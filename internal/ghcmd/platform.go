//go:build !wasip1

package ghcmd

// installPlatformNetworking is where a build that needs a network of its own
// arranges for one. On a normal platform net/http can dial, so there is nothing
// to do; see the wasip1 twin.
func installPlatformNetworking() {}
