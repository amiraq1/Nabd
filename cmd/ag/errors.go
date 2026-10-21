package main

import (
	"errors"
	"fmt"

	"nabd/internal/config"
	"nabd/internal/provider"
	"nabd/internal/registry"
)

// User-facing CLI strings. These are intentionally in Arabic because the
// CLI user is Arabic-speaking. They are exempt from the ASCII-symbol
// whitelist (see ascii_guard_test.go skip list).
const (
	errNoSessions        = "لا جلسات سابقة في %s"
	statusCompacting     = "يضغط السياق…"
	statusSessionEnded   = "جلسة منتهية · %s"
	statusSessionStopped = "أوقفت الجلسة · %s"
	statusSessionFailed  = "فشلت الجلسة · %s"
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
	// Clipboard status notices.
	copySuccessArabic     = "تم نسخ البطاقة المحددة"
	copyUnavailableArabic = "النسخ غير متاح"
	copyBlockedArabic     = "تعذر النسخ: المحتوى كبير جدًا"
	// ownerMismatchNotice renders config.OwnerMismatchError and
	// registry.OwnerMismatchError. Those packages carry the facts on a typed
	// error and stay on the ASCII baseline; the sentence lives here, at the
	// single CLI language boundary (ADR-0002, decision 5).
	ownerMismatchNotice = "%s: يملكه uid %d ولا يملكه %s — شغّل: chown %s %s"
	// providerModelUnavailableArabic and providerFirstByteTimeoutArabic are the
	// provider error wording. internal/provider keeps ASCII English baselines
	// and renders through its language seam (SetModelUnavailableMessage /
	// SetFirstByteTimeoutMessage); installProviderLanguage installs these at
	// startup (ADR-0002 decision 4, provider leg). The values are
	// byte-identical to what internal/provider held before the move.
	providerModelUnavailableArabic = "الموديل %q غير متاح على هذا الخادم (%d)%s"
	providerFirstByteTimeoutArabic = "الخادم لم يبدأ الردّ خلال 25 ثانية — جرّب NABD_MODEL آخر"
)

// ownerNotice carries the Arabic rendering of an ownership refusal while keeping
// the original error reachable through Unwrap, so a caller that inspects the
// cause with errors.Is/errors.As still finds it.
type ownerNotice struct {
	msg   string
	inner error
}

func (e *ownerNotice) Error() string { return e.msg }
func (e *ownerNotice) Unwrap() error { return e.inner }

// localizeOwnerMismatch renders the Arabic ownership refusal for an error raised
// by config or registry, and returns any other error unchanged. It is a display
// concern only: it never classifies, and it never rewrites a message it did not
// recognize.
func localizeOwnerMismatch(err error) error {
	if err == nil {
		return nil
	}
	var c *config.OwnerMismatchError
	if errors.As(err, &c) {
		return &ownerNotice{
			msg:   fmt.Sprintf(ownerMismatchNotice, c.Path, c.OwnerUID, c.Who, c.Who, c.Path),
			inner: err,
		}
	}
	var r *registry.OwnerMismatchError
	if errors.As(err, &r) {
		return &ownerNotice{
			msg:   fmt.Sprintf(ownerMismatchNotice, r.Path, r.OwnerUID, r.Who, r.Who, r.Path),
			inner: err,
		}
	}
	return err
}

// loadConfig loads the configuration file, rendering an ownership refusal in
// Arabic at the CLI boundary. Every CLI path loads through here, so no caller can
// print the ASCII baseline by forgetting to localize.
func loadConfig() error { return localizeOwnerMismatch(config.Load()) }

// loadRegistry loads the provider registry with the same boundary treatment.
func loadRegistry() (*registry.Registry, error) {
	reg, err := registry.Load()
	return reg, localizeOwnerMismatch(err)
}

// installProviderLanguage installs the Arabic provider-error wording into the
// provider package's language seam. It is called once at the top of main,
// before any CLI path can stream a provider answer, so no surface — feed,
// headless stderr, journal, router details — can observe the ASCII baseline.
func installProviderLanguage() {
	provider.SetModelUnavailableMessage(func(model string, status int, suffix string) string {
		return fmt.Sprintf(providerModelUnavailableArabic, model, status, suffix)
	})
	provider.SetFirstByteTimeoutMessage(providerFirstByteTimeoutArabic)
}
