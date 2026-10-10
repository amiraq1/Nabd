#!/usr/bin/env bash
# S4: measure the dependency-tree cost of an MCP Go SDK vs a minimal stdlib-only
# scaffold. Runs in a scratch module under $TMPDIR — never touches the repo's
# go.mod/go.sum.
#
# D5 fix (2026-10-10): the scratch module must IMPORT the SDK before measuring;
# otherwise go list -deps counts 0. Also du the full module cache after
# go mod download all, and count graph edges.
#
# Usage: bash scripts/mcp/s4-sdk-size.sh [sdk-version]
set -u

SDK="${1:-v1.8.0}"
SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

echo "## S4 dependency-tree measurement"
echo "scratch: $SCRATCH"
echo

cd "$SCRATCH"
go mod init scratch >/dev/null 2>&1

echo "### 1) MCP Go SDK ($SDK)"
# D5: import the SDK so deps are real.
mkdir -p mcpimport
cat > mcpimport/main.go <<'EOF'
package main

import (
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {}
EOF
if go get "github.com/modelcontextprotocol/go-sdk@$SDK" >/dev/null 2>&1; then
  go mod tidy >/dev/null 2>&1
  go mod download all >/dev/null 2>&1
  echo "- go list -deps count: $(go list -deps ./... 2>/dev/null | wc -l)"
  echo "- go mod graph edges: $(go mod graph 2>/dev/null | wc -l)"
  echo "- external modules: $(go list -m all 2>/dev/null | grep -v '^scratch' | wc -l)"
  echo "- network deps:"
  go list -deps ./... 2>/dev/null | grep -E '^(net/http|crypto/tls)$' | sed 's/^/  - /'
  echo "- full module cache size:"
  du -sh "$(go env GOMODCACHE)" 2>/dev/null | sed 's/^/  /'
else
  echo "- FAILED to fetch SDK@$SDK (check tag exists and network); inconclusive"
fi
echo

echo "### 2) Minimal stdlib-only scaffold (JSON-RPC lines + pipes)"
rm -rf mcpimport
cat > main.go <<'EOF'
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type req struct {
	ID     any    `json:"id"`
	Method string `json:"method"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": map[string]any{}})
		fmt.Println(string(out))
	}
}
EOF
go mod tidy >/dev/null 2>&1
echo "- go list -deps count: $(go list -deps ./... 2>/dev/null | wc -l)"
echo "- go mod graph edges: $(go mod graph 2>/dev/null | wc -l)"
echo "- external modules: $(go list -m all 2>/dev/null | grep -v '^scratch' | wc -l)"
echo
echo "### Decision input"
echo "Compare the two counts above. If the SDK pulls network/crypto/transitive"
echo "deps, prefer the stdlib scaffold (ADR-0003 §4.5, S4)."
