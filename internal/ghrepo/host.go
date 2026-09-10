//go:build !wasip1

package ghrepo

import "net/url"

// hostFromURL is the host of a remote, as gh names hosts. A port is not part of
// that name: every GitHub host answers on the default one.
func hostFromURL(u *url.URL) string {
	return u.Hostname()
}
