//go:build race

// This file and its !race twin are the standard way to ask the toolchain a
// build question at run time. The timing walls in scan_test.go are skipped under
// the race detector: its overhead does not land on the walk's syscalls and on
// the matcher's string work in the same proportion, so a ratio measured under it
// would not be the ratio those walls claim to bound. ci.yml runs this package
// without -race before it runs it with -race, so the walls are still enforced.
package pathindex

const raceEnabled = true
