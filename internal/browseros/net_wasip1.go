//go:build wasip1

// Package browseros gives gh a network when it runs as a WebAssembly (wasip1)
// process inside BrowserOS (https://github.com/Progate/packages).
//
// # Why gh needs this at all
//
// WASI preview1 has no connect(2). The ABI only knows sock_accept / sock_recv /
// sock_send, which assume the host handed the guest a socket, so Go's wasip1 port
// cannot dial: net.Dial always fails with "not implemented". A gh built for
// wasip1 would therefore have no way to reach any API at all.
//
// BrowserOS closes that hole without extending the ABI: it exposes its socket
// table as a path, the same name bash uses.
//
//	/dev/tcp/<host>/<port>   opening it yields an already connected socket
//
// So dialing is path_open, and the fd that comes back reads and writes like any
// other fd. That is all Dial does below.
//
// # Why the fd is put in non-blocking mode
//
// net/http reads a connection from its own goroutine while the request is still
// being written. A blocking fd_read stops the whole module - wasip1 has no
// threads - so the read loop would park before the request went out and nothing
// would ever complete. Marking the fd non-blocking makes Go register it with the
// poller (poll_oneoff), which parks only the reading goroutine.
//
// # Why TLS is not spoken here
//
// The browser cannot open a raw TCP connection, so BrowserOS reaches the outside
// through a gateway that speaks fetch(), and that gateway is the one that
// terminates TLS: it treats port 443 as https. A guest that wrapped the socket in
// crypto/tls would hand the gateway bytes it cannot read (and wasip1 has no root
// certificates anyway). gh therefore writes plain HTTP/1.1 to port 443 and lets
// the host put it in an https request - the same choice browser-php made for its
// https:// stream wrapper.
package browseros

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DeviceRoot is the directory BrowserOS synthesises for outgoing connections.
const DeviceRoot = "/dev/tcp"

// Available reports whether this process is running on a host that offers the
// socket device. It is false when BrowserOS was started without a network, and
// commands can then explain that instead of failing with a bare ENOENT.
func Available() bool {
	info, err := os.Stat(DeviceRoot)
	return err == nil && info.IsDir()
}

// Dial connects through BrowserOS' socket table. network must be tcp; the
// browser has no other transport to offer.
func Dial(_ context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("browseros: only tcp is supported")}
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: err}
	}
	// The host keeps the name all the way to the gateway: Host, SNI and CORS are
	// all decided by name, so resolving to an address here would lose it.
	host = strings.Trim(host, "[]")
	if host == "" || strings.Contains(host, "/") {
		return nil, &net.OpError{Op: "dial", Net: network, Err: fmt.Errorf("browseros: cannot dial %q", address)}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: fmt.Errorf("browseros: port %q is not a number", port)}
	}

	path := DeviceRoot + "/" + host + "/" + port
	fd, err := syscall.Open(path, syscall.O_RDWR, 0)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Addr: addr(address), Err: dialError(err)}
	}
	// Non-blocking, so that only the goroutine waiting on this fd parks. See the
	// package comment.
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, &net.OpError{Op: "dial", Net: network, Addr: addr(address), Err: err}
	}

	return &conn{file: os.NewFile(uintptr(fd), path), remote: addr(address)}, nil
}

// dialError keeps the errno but says which side of the world it came from, since
// "no such file or directory" reads as a missing file rather than as a refused
// connection.
func dialError(err error) error {
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR):
		if !Available() {
			return errors.New("BrowserOS was started without a network (/dev/tcp is missing)")
		}
		return err
	default:
		return err
	}
}

type addr string

func (a addr) Network() string { return "tcp" }
func (a addr) String() string  { return string(a) }

// conn presents the fd as a net.Conn. Nothing is buffered here: reads and writes
// go straight to BrowserOS' socket table.
type conn struct {
	file   *os.File
	remote addr
}

func (c *conn) Read(b []byte) (int, error)  { return c.file.Read(b) }
func (c *conn) Write(b []byte) (int, error) { return c.file.Write(b) }
func (c *conn) Close() error                { return c.file.Close() }

// LocalAddr is a placeholder. preview1 has no getsockname, so the guest cannot
// learn the port it was given.
func (c *conn) LocalAddr() net.Addr  { return addr("127.0.0.1:0") }
func (c *conn) RemoteAddr() net.Addr { return c.remote }

// Deadlines work because the fd is pollable; on a host that says otherwise the
// deadline is dropped rather than failing the request.
func (c *conn) SetDeadline(t time.Time) error     { return ignoreNoDeadline(c.file.SetDeadline(t)) }
func (c *conn) SetReadDeadline(t time.Time) error { return ignoreNoDeadline(c.file.SetReadDeadline(t)) }
func (c *conn) SetWriteDeadline(t time.Time) error {
	return ignoreNoDeadline(c.file.SetWriteDeadline(t))
}

func ignoreNoDeadline(err error) error {
	if errors.Is(err, os.ErrNoDeadline) {
		return nil
	}
	return err
}
