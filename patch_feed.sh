sed -i 's/prog \*tea.Program/prog \*tea.Program\n\ttestSyncDispatch bool/' internal/ui/feed.go
sed -i 's/\/\/ Test path: no live program; apply directly. Tests call SendBatch from/if !m.testSyncDispatch {\n\t\tpanic("SendBatch called without a running program (m.prog == nil) outside of tests")\n\t}\n\t\/\/ Test path: no live program; apply directly. Tests call SendBatch from/' internal/ui/feed.go
