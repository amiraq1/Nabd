// Package agent: squeeze.go drops stale tool output before paying a model to
// summarise it. Recent rounds stay verbatim; older ones keep their identity
// and lose their body. Errors are never squeezed: they are short and they
// carry the lesson that stops the model repeating itself.
package agent

import (
	"fmt"

	"nabd/internal/provider"
)

const (
	KeepFullRounds = 3
	stubOver       = 400 // characters below this cost less than the stub
)

func Squeeze(ms []provider.Message, keepRounds int) []provider.Message {
	rounds, cut := 0, -1
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Role != provider.User {
			continue
		}
		rounds++
		if rounds > keepRounds {
			cut = i
			break
		}
	}
	if cut < 0 {
		return ms
	}
	out := make([]provider.Message, len(ms))
	copy(out, ms)
	for i := 0; i < cut; i++ {
		if len(out[i].ToolResults) == 0 {
			continue
		}
		rs := make([]provider.ToolResult, len(out[i].ToolResults))
		copy(rs, out[i].ToolResults)
		for j, r := range rs {
			if r.IsErr || len(r.Output) < stubOver {
				continue
			}
			rs[j].Output = fmt.Sprintf("«%d بايت من المخرَج أُزيلت لتوفير السياق؛ أعد النداء إن احتجتها»", len(r.Output))
		}
		out[i].ToolResults = rs
	}
	return out
}
