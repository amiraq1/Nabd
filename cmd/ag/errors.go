package main

// User-facing CLI strings. These are intentionally in Arabic because the
// CLI user is Arabic-speaking. They are exempt from the ASCII-symbol
// whitelist (see ascii_guard_test.go skip list).
const (
	errNoSessions      = "لا جلسات سابقة في %s"
	statusCompacting   = "يضغط السياق…"
	statusSessionEnded = "جلسة منتهية · %s"
	// limitNoticeArabic is the composer input-limit notice shown in the
	// feed UI status line. internal/ui keeps its own ASCII default because
	// its string literals must stay on the ASCII-symbol whitelist; the CLI
	// installs the Arabic form here at startup.
	limitNoticeArabic = "تجاوز الإدخال الحد الأقصى: 8000 محرف أو 200 سطر."
	// conflictNotice is the single line printed when the file silently
	// overrode environment keys. Kept here so main.go stays on the
	// ASCII-symbol whitelist.
	conflictNotice = "تجاهلتُ من البيئة (الأسبقية لـ~/.ag/config): %s"
	// conflictSep is the Arabic comma separator for conflict key lists.
	conflictSep = "، "
)
