//go:build wasip1

package browse

import "errors"

// ExtBrowse is not built into a WebAssembly gh. The interface it runs is a
// full-screen terminal application (tview on tcell), which reads the terminal
// through termios and draws with terminfo - neither of which wasip1 has.
//
// The commands that do the same work without taking over the terminal are
// unaffected: gh ext search, gh ext install, gh ext remove.
func ExtBrowse(ExtBrowseOpts) error {
	return errors.New("gh ext browse needs a terminal it can take over; try gh ext search instead")
}
