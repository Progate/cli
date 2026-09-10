//go:build !wasip1

package ghinstance

import (
	"errors"
	"strings"
)

// A GitHub host is a bare domain name. A port would not survive the way gh
// stores hosts and builds URLs from them, and a slash means the user pasted a
// URL rather than a hostname.
func validateHostname(hostname string) error {
	if strings.ContainsRune(hostname, '/') || strings.ContainsRune(hostname, ':') {
		return errors.New("invalid hostname")
	}
	return nil
}
