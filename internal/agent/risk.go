// Package agent: risk.go defines the RiskClass type and risk levels used by
// the advisory risk classifier. These types are consumed by perm/risk.go and
// the headless gate wrapper.
package agent

// RiskLevel is the advisory classification of a shell command. It never blocks
// and never auto-denies — it only changes how the permission prompt is rendered.
type RiskLevel string

const (
	// RiskLow is the default classification for benign commands.
	RiskLow RiskLevel = "low"
	// RiskUnknown is returned when the command cannot be classified (e.g. command
	// substitution as the command name).
	RiskUnknown RiskLevel = "unknown"
	// RiskDestructive flags commands that may destroy data or filesystem state.
	RiskDestructive RiskLevel = "destructive"
	// RiskExfiltrating flags commands that may send data to external hosts.
	RiskExfiltrating RiskLevel = "exfiltrating"
)

// riskOrder defines the ordering of risk levels for Max comparison.
var riskOrder = map[RiskLevel]int{
	RiskLow:          0,
	RiskUnknown:      1,
	RiskDestructive:  2,
	RiskExfiltrating: 3,
}

// RiskClass is the advisory classification result for a shell command. Level
// is the severity; Reason is a human-readable explanation of why the command
// was classified at that level.
type RiskClass struct {
	Level  RiskLevel
	Reason string
}

// Max returns the higher-risk of the two classifications. If levels are equal,
// the receiver is returned.
func (r RiskClass) Max(other RiskClass) RiskClass {
	if riskOrder[other.Level] > riskOrder[r.Level] {
		return other
	}
	return r
}

// riskClassifier is the optional interface a Gate can implement to provide
// advisory risk classification over bash commands. The loop's optional-interface
// assertion finds it through the Gate wrapper.
type riskClassifier interface {
	Classify(cmd string) RiskClass
	IsNetwork(cmd string) bool
	Offline() bool
}

// CheckpointRecord is the persisted metadata for a git checkpoint created
// before a mutating tool call. It captures the commit/tree/head OIDs, branch,
// command text, file count, and duration so the checkpoint can be diffed or
// restored later.
type CheckpointRecord struct {
	Commit     string
	Tree       string
	Head       string
	Branch     string
	Command    string
	Files      int
	DurationMS int64
}
