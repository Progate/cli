//go:build !wasip1

package git

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/cli/safeexec"
)

// resolveGitPath finds the git executable once, so that every command after it
// starts without searching PATH again.
func resolveGitPath() (string, error) {
	path, err := safeexec.LookPath("git")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			programName := "git"
			if runtime.GOOS == "windows" {
				programName = "Git for Windows"
			}
			return "", &NotInstalled{
				message: fmt.Sprintf("unable to find git executable in PATH; please install %s before retrying", programName),
				err:     err,
			}
		}
		return "", err
	}
	return path, nil
}
