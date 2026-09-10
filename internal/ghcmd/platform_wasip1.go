//go:build wasip1

package ghcmd

import "github.com/cli/cli/v2/internal/browseros"

// installPlatformRuntime gives the WebAssembly build the two things wasip1 does
// not have and gh cannot work without (see internal/browseros).
//
//   - a network: Go's wasip1 port cannot dial, because preview1 has no connect(2)
//   - a way to run git: it has no fork and no exec either
//
// Both come from BrowserOS as paths under /dev, and both are installed into the
// hooks the rest of gh already goes through (http.DefaultTransport and
// run.PrepareCmd), so no command needs to know where it is running.
func installPlatformRuntime() {
	browseros.InstallTransport()
	browseros.InstallExec()
}
