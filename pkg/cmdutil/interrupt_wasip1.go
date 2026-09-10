//go:build wasip1

package cmdutil

import "errors"

// InterruptErr is declared without survey here. A WebAssembly build cannot use
// survey at all: its reader needs termios, and wasip1 has no ioctl. The
// line-based prompter (internal/prompter) never raises this, because a browser
// terminal delivers Ctrl+C as a signal to the process rather than as a rune in
// the input stream - but commands still test for it, so the sentinel exists.
var InterruptErr = errors.New("interrupt")
