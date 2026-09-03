package main

// User-facing CLI strings. These are intentionally in Arabic because the
// CLI user is Arabic-speaking. They are exempt from the ASCII-symbol
// whitelist (see ascii_guard_test.go skip list).
const (
	errNoSessions = "لا جلسات سابقة في %s"
	statusCompacting = "يضغط السياق…"
	statusSessionEnded = "جلسة منتهية · %s"
)
