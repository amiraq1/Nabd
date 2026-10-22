package endpoint

import (
	"testing"
	"time"
)

// P0-1 regression: provider clients must refuse redirects so x-api-key
// is never forwarded to a redirect target.
func TestP01_CheckRedirectRefuses(t *testing.T) {
	client := ClientWithAllow(Policy(0), CurrentAllowList(), 5*time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set (P0-1)")
	}
	// noRedirect must refuse regardless of request details.
	if err := client.CheckRedirect(nil, nil); err == nil {
		t.Error("CheckRedirect must refuse redirects")
	}
}
