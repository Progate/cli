//go:build wasip1

package git

import "github.com/cli/cli/v2/internal/browseros"

// resolveGitPath does not look for a file on a WebAssembly build: the programs
// BrowserOS carries are entries in the kernel's table, not files with an
// executable bit, and the OS is what resolves a name against PATH when it starts
// a process (see internal/browseros). Asking here would find nothing and refuse
// to run a git that is in fact available.
//
// What is worth checking is whether this BrowserOS can start processes at all;
// if it cannot, saying so now is better than a "command not found" later.
func resolveGitPath() (string, error) {
	path, err := browseros.LookPathHint("git")
	if err != nil {
		return "", &NotInstalled{message: err.Error(), err: err}
	}
	return path, nil
}
