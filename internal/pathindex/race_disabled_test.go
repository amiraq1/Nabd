//go:build !race

// See race_enabled_test.go: this twin gives the untagged tests the plain build
// in which the timing walls apply.
package pathindex

const raceEnabled = false
