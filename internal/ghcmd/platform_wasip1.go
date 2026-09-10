//go:build wasip1

package ghcmd

import "github.com/cli/cli/v2/internal/browseros"

// installPlatformNetworking gives the WebAssembly build a network. Without it
// every request fails, because Go's wasip1 port cannot dial: WASI preview1 has
// no connect(2) (see internal/browseros).
func installPlatformNetworking() {
	browseros.InstallTransport()
}
