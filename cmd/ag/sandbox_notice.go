package main

import "io"

// sandboxAuthorityNotice is deliberately conservative. Landlock is
// host/kernel-dependent and approved bash still runs with the current user's
// authority on fallback paths; this is a notice, not a security guarantee.
const sandboxAuthorityNotice = "notice: approved bash commands run with the current user authority; filesystem sandboxing is host-dependent and is not a complete security boundary\n"

func writeSandboxAuthorityNotice(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, sandboxAuthorityNotice)
}
