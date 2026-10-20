//go:build wasip1

package browseros

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
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
//
// It is an ordinary http.Transport: http:// goes out as plain HTTP/1.1, and
// https:// is TLS spoken by crypto/tls over the same /dev/tcp socket, with the
// server's certificate verified against the system roots (see systemRoots).
func NewTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:    Dial,
		DialTLSContext: DialTLS,
		// The gateway on the host closes the connection after each exchange, so a
		// pool of idle connections would only hold sockets the host has dropped.
		DisableKeepAlives:     keepAlivesDisabled(),
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 0,
		// No HTTP/2: BrowserOS' egress proxy hands the gateway one HTTP/1.1
		// exchange at a time, and only offers http/1.1 in ALPN.
		ForceAttemptHTTP2: false,
	}
}

// keepAlivesDisabled lets a host that does keep connections alive (a server
// running next door in browser-node, for instance) opt back in.
func keepAlivesDisabled() bool {
	return os.Getenv("GH_BROWSEROS_KEEP_ALIVE") == ""
}

// DialTLS opens a socket with Dial and runs a TLS handshake over it.
//
// http.Transport would do the same with TLSClientConfig, but the roots have to
// be in that config before the first request, and reading them at startup would
// make every gh command - `gh --version` included - parse the CA bundle. Dialing
// here lets them be loaded on the first https request instead.
func DialTLS(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: err}
	}
	// Dial first, so that a BrowserOS without a network or a refused connection
	// is reported as such rather than as missing roots.
	raw, err := Dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	roots, err := systemRoots()
	if err != nil {
		_ = raw.Close()
		return nil, &net.OpError{Op: "dial", Net: network, Addr: addr(address), Err: err}
	}
	conn := tls.Client(raw, &tls.Config{
		// The name the caller asked for, so that SNI and verification are about
		// that host. crypto/tls leaves SNI out by itself when it is an address.
		ServerName: strings.Trim(host, "[]"),
		RootCAs:    roots,
		NextProtos: []string{"http/1.1"},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// linuxCertFiles is the list crypto/x509 searches on Linux (root_linux.go).
//
// On wasip1 crypto/x509 searches nothing: root_wasm.go leaves both lists empty,
// so without SSL_CERT_FILE every certificate would be "signed by unknown
// authority". BrowserOS lays out /etc/ssl the way Debian does, so gh looks where
// a Linux gh would.
var linuxCertFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian/Ubuntu/Gentoo etc.
	"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora/RHEL 6
	"/etc/ssl/ca-bundle.pem",                            // OpenSUSE
	"/etc/pki/tls/cacert.pem",                           // OpenELEC
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS/RHEL 7
	"/etc/ssl/cert.pem",                                 // Alpine Linux
}

// systemRoots returns the roots https servers are verified against, loaded once.
//
// SSL_CERT_FILE and SSL_CERT_DIR mean what they mean on Linux, and crypto/x509
// already honours them on wasip1, so it is asked directly when either is set.
// Otherwise the first bundle in linuxCertFiles is used. Unlike Linux, the
// directories (/etc/ssl/certs) are not read on top: on Debian the bundle is
// built from the same certificates.
var systemRoots = sync.OnceValues(func() (*x509.CertPool, error) {
	if os.Getenv("SSL_CERT_FILE") != "" || os.Getenv("SSL_CERT_DIR") != "" {
		return x509.SystemCertPool()
	}
	for _, name := range linuxCertFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(data)
		return pool, nil
	}
	return nil, errors.New("browseros: no root certificates (" + linuxCertFiles[0] + " is missing; SSL_CERT_FILE names another bundle)")
})
