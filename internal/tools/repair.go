package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"nabd/internal/provider"
)

// Tool-call repair (NBD-420).
//
// Open models write malformed tool calls. A malformed call costs a whole
// round: the request goes out, an error comes back, and a second request
// fixes it. Rounds are the dominant term in the cost of a turn — NBD-401
// measured a 5.00x request-count spread against 2.85x for history, because
// Squeeze trims the history and cannot touch the fixed payload multiplied by
// the request count. This layer exists to remove rounds; a rule that saves no
// measured round is deleted (see repair_rounds_test.go).
//
// Repair is pure: no disk, no network, no global state, deterministic for the
// same inputs. It never guesses intent — it removes a wrapper, matches an
// explicit alias, follows the tool's own schema, or leaves the call alone.
//
// It is also deliberately narrow. An inference that widens a call's reach is
// worse than an error: an error costs a round, while a wrong write costs the
// user's file. So the direction of inference is one-way — see readOnlyTools.

// Fix records one correction. Field names what was changed ("name" for the
// tool name, otherwise the argument key), Was and Now are the values either
// side of the change, and Rule names the rule that fired.
type Fix struct {
	Field string
	Was   string
	Now   string
	Rule  string
}

// maxFixes caps corrections per call. A call needing more than this is not
// malformed, it is a different call; it is returned unchanged and the model
// reads an error, which is a path that works.
const maxFixes = 3

// Rule names. They are stable strings because they appear in journal Notices.
const (
	RuleToolAlias    = "tool-name-alias"
	RuleUnwrapString = "unwrap-json-string"
	RuleUnwrapText   = "unwrap-wrapper-text"
	RuleFieldAlias   = "field-alias"
	RuleIntegerText  = "integer-as-string"
	RuleMarkdownPath = "markdown-link-path"
)

// readOnlyTools is the only set a name may be inferred to. Inference exists
// because an unrecognised name is often a spelling of a ReadOnly tool, and the
// asymmetry is deliberate: inferring toward ReadOnly costs a round when wrong,
// while inferring toward write_file, edit_file or bash could overwrite a file
// or run a command the model never asked for. Those names require a literal
// match and are never inferred.
//
// TestRepairInferenceIsReadOnlyOnly checks this against Registry.Class rather
// than against a second written list, so the two cannot drift.
var readOnlyTools = map[string]bool{
	"read_file": true,
	"glob":      true,
	"grep":      true,
}

// toolAliases maps an explicit, observed misspelling to a declared tool name.
//
// This is a map, never edit distance or string similarity: read_file and
// write_file differ by one character and mean opposite things. Every target
// must be declared in `known` and must be ReadOnly — both are asserted by
// tests, so a bad entry is a failing build rather than a runtime surprise.
var toolAliases = map[string]string{
	"read":        "read_file",
	"readfile":    "read_file",
	"read_text":   "read_file",
	"cat":         "read_file",
	"open_file":   "read_file",
	"ls":          "glob",
	"list":        "glob",
	"list_files":  "glob",
	"find_files":  "glob",
	"search":      "grep",
	"search_text": "grep",
	"rg":          "grep",
}

// fieldAliases maps an argument-key misspelling to the declared field it
// stands for. This is knowledge about model errors, not about tools: whether
// the target exists is decided by the tool's own schema, so a tool that does
// not declare the field is never rewritten.
var fieldAliases = map[string]string{
	"file":      "path",
	"filename":  "path",
	"filepath":  "path",
	"file_path": "path",
	"pathname":  "path",

	"command":   "cmd",
	"shell":     "cmd",
	"script":    "cmd",
	"bash":      "cmd",
	"shell_cmd": "cmd",

	"regex":  "pattern",
	"query":  "pattern",
	"search": "pattern",

	"contents": "content",
	"body":     "content",
}

// pathLikeFields are the declared string fields whose value may be written as
// a Markdown link. Extraction is restricted to these: running it over every
// string field would rewrite a file's content because it happened to contain a
// link, which is a silent corruption rather than a repair.
var pathLikeFields = map[string]bool{"path": true}

// markdownLinkRE matches a whole value that is a Markdown link, capturing the
// target.
var markdownLinkRE = regexp.MustCompile(`^\[[^\]]*\]\(([^)]+)\)$`)

// Repair corrects a malformed tool call against the declared specs.
//
// It returns the corrected name and arguments plus the fixes it applied. When
// nothing is wrong, or when correcting would need more than maxFixes changes,
// the inputs are returned unchanged with no fixes.
func Repair(name string, raw json.RawMessage, known []provider.ToolSpec) (string, json.RawMessage, []Fix) {
	spec, declared := specByName(known, name)

	// Rule: tool-name alias. Only an explicit map entry, only a declared
	// target, only a ReadOnly one.
	fixedName := name
	var nameFixes []Fix
	if !declared {
		if target, ok := toolAliases[name]; ok {
			if _, targetDeclared := specByName(known, target); targetDeclared && readOnlyTools[target] {
				nameFixes = append(nameFixes, Fix{Field: "name", Was: name, Now: target, Rule: RuleToolAlias})
				fixedName = target
				spec, declared = specByName(known, target)
			}
		}
	}

	// Only a declared tool can have its arguments repaired: the schema is the
	// only thing that says what a field is called and what type it has.
	if !declared {
		return name, raw, nil
	}

	args, argFixes := repairArgs(raw, spec)

	fixes := append(append([]Fix{}, nameFixes...), argFixes...)
	if len(fixes) == 0 {
		return name, raw, nil
	}
	if len(fixes) > maxFixes {
		// Not malformed — different. Return the call as written.
		return name, raw, nil
	}
	return fixedName, args, fixes
}

// repairArgs removes wrappers around the payload and then corrects fields
// against the schema.
func repairArgs(raw json.RawMessage, spec provider.ToolSpec) (json.RawMessage, []Fix) {
	var fixes []Fix
	payload := raw

	// Wrapper rules: remove one layer at a time until the payload is an
	// object. The loop is bounded by maxFixes and never re-enters Repair, so
	// the output cannot feed back into the input.
	for i := 0; i < maxFixes; i++ {
		if _, ok := decodeObject(payload); ok {
			break
		}
		if s, ok := unwrapStringLiteral(payload); ok {
			payload = s
			fixes = append(fixes, Fix{Field: "payload", Was: "json string", Now: "json object", Rule: RuleUnwrapString})
			continue
		}
		if s, ok := outermostObject(payload); ok {
			payload = s
			fixes = append(fixes, Fix{Field: "payload", Was: "wrapped text", Now: "json object", Rule: RuleUnwrapText})
			continue
		}
		break
	}

	obj, ok := decodeObject(payload)
	if !ok {
		return raw, nil
	}

	fieldFixes, changed := repairFields(obj, spec)
	if !changed {
		if len(fixes) == 0 {
			return raw, nil
		}
		// Wrapper fixes only: the unwrapped payload is the correction, and it
		// is already valid JSON from the unwrap step.
		return payload, fixes
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw, nil
	}
	return out, append(fixes, fieldFixes...)
}

// repairFields applies the schema-driven field corrections and reports whether
// the object changed.
func repairFields(obj map[string]any, spec provider.ToolSpec) ([]Fix, bool) {
	sch := parseSchema(spec.Schema)
	if len(sch.Properties) == 0 {
		return nil, false
	}

	var fixes []Fix
	changed := false

	// Renames are collected first and applied after the scan, so the iteration
	// never mutates the map it is walking and the result is order-independent.
	type rename struct{ from, to string }
	var renames []rename
	for key := range obj {
		if _, declaredField := sch.Properties[key]; declaredField {
			continue
		}
		target, ok := fieldAliases[key]
		if !ok {
			continue
		}
		if _, targetDeclared := sch.Properties[target]; !targetDeclared {
			continue
		}
		if _, clash := obj[target]; clash {
			// Both spellings present: which one the model meant is a guess,
			// and guessing here could point the call at a different file.
			continue
		}
		renames = append(renames, rename{from: key, to: target})
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].from < renames[j].from })
	for _, rn := range renames {
		obj[rn.to] = obj[rn.from]
		delete(obj, rn.from)
		// For a rename, Was/Now name the field: the declared field it became,
		// and the spelling the model used.
		fixes = append(fixes, Fix{Field: rn.to, Was: rn.from, Now: rn.to, Rule: RuleFieldAlias})
		changed = true
	}

	for key, val := range obj {
		prop, declaredField := sch.Properties[key]
		if !declaredField {
			continue
		}

		// Declared integer, sent as text.
		if prop.Type == "integer" {
			if s, isString := val.(string); isString {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					obj[key] = n
					fixes = append(fixes, Fix{Field: key, Was: shortValue(val), Now: shortValue(n), Rule: RuleIntegerText})
					changed = true
					continue
				}
			}
		}

		// A path-like field written as a Markdown link.
		if prop.Type == "string" && pathLikeFields[key] {
			if s, isString := val.(string); isString {
				if m := markdownLinkRE.FindStringSubmatch(s); m != nil && m[1] != "" {
					obj[key] = m[1]
					fixes = append(fixes, Fix{Field: key, Was: shortValue(s), Now: shortValue(m[1]), Rule: RuleMarkdownPath})
					changed = true
				}
			}
		}
	}

	// The scan above walks a map, so the fixes are ordered last for
	// determinism: two runs on the same input produce the same list.
	sort.SliceStable(fixes, func(i, j int) bool {
		if fixes[i].Rule != fixes[j].Rule {
			return fixes[i].Rule < fixes[j].Rule
		}
		return fixes[i].Field < fixes[j].Field
	})
	return fixes, changed
}

// typeSchema is the subset of input_schema this layer reads. Unknown keywords
// are ignored: the layer follows the schema, it does not validate it.
type typeSchema struct {
	Properties map[string]struct {
		Type string `json:"type"`
	} `json:"properties"`
}

func parseSchema(raw json.RawMessage) typeSchema {
	var s typeSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return typeSchema{}
	}
	return s
}

// specByName finds a declared spec by exact name.
func specByName(known []provider.ToolSpec, name string) (provider.ToolSpec, bool) {
	for _, s := range known {
		if s.Name == name {
			return s, true
		}
	}
	return provider.ToolSpec{}, false
}

// decodeObject parses a payload expected to be a JSON object.
func decodeObject(raw json.RawMessage) (map[string]any, bool) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// unwrapStringLiteral removes one layer of string encoding: a payload that
// arrived as a JSON string holding JSON rather than as the object itself. The
// inner text must itself be valid JSON, so an ordinary string argument is never
// unwrapped. Nested encoding is handled by the caller's bounded loop, not by
// recursion here.
func unwrapStringLiteral(raw json.RawMessage) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}

// outermostObject extracts the span from the first opening brace to the last
// closing one, for payloads carrying prose around the object. It returns false
// when that span is not a JSON object, so text is only ever dropped when what
// remains parses.
//
// The wrapper shapes this recovers from — a payload quoted inside a fake
// tool-call envelope, or fenced as code — are deliberately described rather
// than written anywhere in this repository: a literal of either shape would be
// misread by any layer that scans for it.
func outermostObject(raw json.RawMessage) (json.RawMessage, bool) {
	s := string(raw)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return nil, false
	}
	span := json.RawMessage(strings.TrimSpace(s[start : end+1]))
	if _, ok := decodeObject(span); !ok {
		return nil, false
	}
	return span, true
}

// noticeValueCap bounds how much of a value a Fix may carry into a journal
// Notice. Notice text is displayed and stored; the full argument of a call can
// be a file body, so values are shortened and control characters stripped
// rather than copied.
const noticeValueCap = 48

// shortValue bounds a value for a Notice.
func shortValue(v any) string {
	s := fmt.Sprint(v)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > noticeValueCap {
		s = s[:noticeValueCap] + "…"
	}
	return s
}

// Notice renders a Fix as the journal line for it. It carries the rule, the
// field and both values — shortened and with control characters removed, so a
// call carrying a file body cannot put that body in the journal. The bounds are
// applied here rather than only where a Fix is built, so a Fix constructed
// anywhere still cannot leak a whole value into the journal.
func (f Fix) Notice() string {
	return fmt.Sprintf("repair: %s · %s %q → %q", f.Rule, f.Field, shortValue(f.Was), shortValue(f.Now))
}
