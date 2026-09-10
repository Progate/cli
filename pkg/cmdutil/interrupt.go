//go:build !wasip1

package cmdutil

import "github.com/AlecAivazis/survey/v2/terminal"

// InterruptErr is what a prompt returns when the user interrupts it (Ctrl+C).
// It comes from survey, which is the library that reads the terminal on this
// platform; see the wasip1 twin for the build that has no terminal library.
var InterruptErr = terminal.InterruptErr
