//go:build wasip1

package browseros

import (
	"fmt"
	"os"
	"syscall"
)

// Opening a connection. WASI preview1 has no connect(2), so BrowserOS exposes
// its socket table as a path - the same name bash uses, /dev/tcp/<host>/<port> -
// and opening it yields a connected socket.
//
// What is opened here is non-blocking. That is not an optimisation: a blocking
// fd_read stops the whole module, since wasip1 has no threads, and net/http
// reads a connection from one goroutine while another writes to it.
// Non-blocking fds are registered with Go's poller, which parks only the
// goroutine that is waiting.

func openDevice(path string, flag int) (*os.File, error) {
	fd, err := syscall.Open(path, flag, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, &os.PathError{Op: "setnonblock", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// missingDevice explains an ENOENT on a device path. Without this the error
// reads as a missing file, when what it means is that this BrowserOS was started
// without that capability.
func missingDevice(root string, what string, err error) error {
	if _, statErr := os.Stat(root); statErr != nil {
		return fmt.Errorf("BrowserOS was started without %s (%s is missing)", what, root)
	}
	return err
}
