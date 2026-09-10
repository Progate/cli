//go:build wasip1

package ghinstance

import (
	"errors"
	"strconv"
	"strings"
)

// A WebAssembly build also accepts a port: host:port.
//
// Every host gh normally talks to answers on 443, but inside BrowserOS the
// interesting one is a server the user just started next door - in browser-node,
// say - and that server is on whatever port it was given. Without a port there
// would be no way to name it, and gh would only be able to reach the outside
// world.
//
// The rest of gh needs no changes for this: a host with a port is not
// github.com, so gh treats it as GitHub Enterprise and asks
// https://host:port/api/v3/ and https://host:port/api/graphql, and the wasip1
// transport (internal/browseros) keeps that port when it opens the socket.
func validateHostname(hostname string) error {
	if strings.ContainsRune(hostname, '/') {
		return errors.New("invalid hostname")
	}

	host, port, found := strings.Cut(hostname, ":")
	if !found {
		return nil
	}
	if host == "" {
		return errors.New("invalid hostname")
	}
	if strings.ContainsRune(port, ':') {
		return errors.New("invalid hostname")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("invalid port")
	}
	return nil
}
