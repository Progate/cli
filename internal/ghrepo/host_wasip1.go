//go:build wasip1

package ghrepo

import "net/url"

// hostFromURL keeps the port on a WebAssembly build, because there a host may
// have one: the interesting server is often one the user just started next door
// (see internal/ghinstance). Dropping it here would make gh compare
// "127.0.0.1" against the "127.0.0.1:3000" it was configured with, and decide
// that none of the repository's remotes belong to the host it knows.
func hostFromURL(u *url.URL) string {
	return u.Host
}
