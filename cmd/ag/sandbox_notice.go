package main

import "io"

// sandboxAuthorityNotice states plainly that on Termux, approved bash commands
// run with the full authority of the Termux app user and there is no
// filesystem sandbox.
const sandboxAuthorityNotice = "notice: approved bash commands run with the full authority of the Termux app user and there is no filesystem sandbox\n"

func writeSandboxAuthorityNotice(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, sandboxAuthorityNotice)
}
