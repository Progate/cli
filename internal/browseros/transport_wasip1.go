//go:build wasip1

package browseros

import (
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// InstallTransport points net/http.DefaultTransport at BrowserOS' socket table.
//
// Replacing the default is deliberate rather than threading a dialer through
// gh's own client: go-gh builds its client on http.DefaultTransport, and so do
// the parts of gh that talk to a host directly. One replacement covers all of
// them, and anything that builds its own http.Transport is a bug on wasip1
// anyway, because such a client would try to dial and fail.
func InstallTransport() {
	http.DefaultTransport = NewTransport()
}

// NewTransport returns the RoundTripper gh uses inside BrowserOS.
func NewTransport() http.RoundTripper {
	return &plaintext{base: &http.Transport{
		DialContext: Dial,
		// The gateway on the host closes the connection after each exchange, so a
		// pool of idle connections would only hold sockets the host has dropped.
		DisableKeepAlives:     keepAlivesDisabled(),
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 0,
		// No HTTP/2: the gateway speaks HTTP/1.1 and there is no ALPN without TLS.
		ForceAttemptHTTP2: false,
	}}
}

// keepAlivesDisabled lets a host that does keep connections alive (a server
// running next door in browser-node, for instance) opt back in.
func keepAlivesDisabled() bool {
	return os.Getenv("GH_BROWSEROS_KEEP_ALIVE") == ""
}

// plaintext sends every request as plain HTTP/1.1 and lets the host add TLS.
//
// The port is kept: BrowserOS decides at the gateway that port 443 means https,
// so an https URL has to arrive as a request to port 443 - only without the
// encryption the guest cannot do (see the package comment).
type plaintext struct {
	base http.RoundTripper
}

func (p *plaintext) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return p.base.RoundTrip(req)
	}

	// The request must not be mutated: net/http may retry it, and the caller
	// still owns it.
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	if out.URL.Port() == "" {
		out.URL.Host = net.JoinHostPort(out.URL.Hostname(), "443")
	}
	// Host stays the name the caller asked for, without the port, so the server
	// on the other side sees the same header it would see over TLS.
	if out.Host == "" {
		out.Host = req.URL.Host
	}
	out.Host = strings.TrimSuffix(out.Host, ":443")

	res, err := p.base.RoundTrip(out)
	if res != nil {
		// Anything reading Response.Request (redirects, error messages) should see
		// the URL the caller asked for, not the downgraded one.
		res.Request = req
	}
	return res, err
}
