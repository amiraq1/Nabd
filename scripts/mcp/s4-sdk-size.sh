#!/usr/bin/env bash
# S4: measure the dependency-tree cost of an MCP Go SDK vs a minimal stdlib-only
# scaffold. Runs in a scratch module under $TMPDIR — never touches the repo's
# go.mod/go.sum.
#
# Usage: bash scripts/mcp/s4-sdk-size.sh [sdk-version]
# Default SDK: github.com/modelcontextprotocol/go-sdk (pinned tag below).
set -u

SDK="${1:-v0.2.0}"
SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

echo "## S4 dependency-tree measurement"
echo "scratch: $SCRATCH"
echo

cd "$SCRATCH"
go mod init scratch >/dev/null 2>&1

echo "### 1) MCP Go SDK ($SDK)"
if go get "github.com/modelcontextprotocol/go-sdk@$SDK" >/dev/null 2>&1; then
  echo "- go list -deps count: $(go list -deps ./... 2>/dev/null | wc -l)"
  echo "- go mod graph edges: $(go mod graph 2>/dev/null | wc -l)"
  echo "- network deps (net/http, crypto/tls):"
  go list -deps ./... 2>/dev/null | grep -E "^(net/http|crypto/tls)$" | sed 's/^/  - /'
  echo "- module cache size for pulled modules:"
  du -sh "$(go env GOMODCACHE)/github.com/modelcontextprotocol" 2>/dev/null | sed 's/^/  /'
else
  echo "- FAILED to fetch SDK@$SDK (offline?); record as inconclusive"
fi
echo

echo "### 2) Minimal stdlib-only scaffold (JSON-RPC lines + pipes)"
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
echo "- external modules: $(go list -m all 2>/dev/null | grep -v "^scratch" | wc -l)"
echo
echo "### Decision input"
echo "Compare the two counts above. If the SDK pulls network/crypto/transitive"
echo "deps, prefer the stdlib scaffold (ADR-0003 §4.5, S4)."
