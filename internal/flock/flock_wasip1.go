//go:build wasip1

package flock

import "os"

// TryLock does not lock on wasip1: the ABI has no flock(2), and there is no
// second process to lock against. A WebAssembly build of gh is one process in
// one browser tab, and the files this guards (the config, the extension list)
// are already private to it.
//
// The file is still opened and returned, because callers read and write through
// it rather than reopening the path.
func TryLock(path string) (f *os.File, unlock func(), err error) {
	f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
