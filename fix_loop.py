import re

with open("internal/agent/loop.go", "r") as f:
    content = f.read()

# 1. Add hist field and remove msgs
content = re.sub(r'msgs\s+\[\]provider\.Message', r'hist   []Event', content)

# 2. Remove l.append(provider.Message{...}) calls
content = re.sub(r'\s*l\.append\(provider\.Message\{[^}]*\}\)', '', content)

# 3. Change snapshot() to Messages(Live(l.hist))
content = re.sub(r'l\.snapshot\(\)', r'Messages(Live(l.hist))', content)

# 4. Remove append and snapshot methods
content = re.sub(r'func \(l \*Loop\) append.*?\}', '', content, flags=re.DOTALL)
content = re.sub(r'func \(l \*Loop\) snapshot.*?\}', '', content, flags=re.DOTALL)

# 5. Change emit to use emitAt
old_emit = """func (l *Loop) emit(e Event) error {
	l.mu.Lock()
	l.seq++
	e.Seq = l.seq
	e.Parent = l.parent
	l.parent = l.seq
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	l.mu.Unlock()

	if l.Sink == nil {
		return nil
	}
	return l.Sink.Emit(e)
}"""

new_emit = """func (l *Loop) emit(e Event) error {
	l.emitAt(l.parent, e)
	return nil
}"""

content = content.replace(old_emit, new_emit)

# 6. Add Seed method
seed_method = """

// Seed adopts a previous branch as this run's history. The new journal
// starts empty on purpose: sessions stay separate files, the tree lives in
// memory. Merging files is a v0.8 problem, not a v0.7 one.
func (l *Loop) Seed(evs []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hist = append(l.hist, evs...)
	if n := len(evs); n > 0 {
		l.seq = evs[n-1].Seq
		l.parent = evs[n-1].Seq
	}
}
"""
content += seed_method

with open("internal/agent/loop.go", "w") as f:
    f.write(content)

