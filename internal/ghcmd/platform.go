//go:build !wasip1

package ghcmd

// installPlatformRuntime is where a build that cannot dial or start processes
// arranges for both. On a normal platform net/http and os/exec work, so there is
// nothing to do; see the wasip1 twin.
func installPlatformRuntime() {}
