//go:build wasip1

package browseros

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Running another program from inside a WebAssembly guest.
//
// # Why this is not os/exec
//
// wasip1 has no fork and no exec. Go's os/exec compiles, but every Start()
// returns ENOSYS, so gh cannot shell out to git - and a gh that cannot run git
// is missing half of itself (pr create, repo clone, repo create --push, and
// everything else that reads the local repository).
//
// BrowserOS supplies the calls preview1 is missing as host functions, and they
// are the POSIX ones, with the POSIX shapes:
//
//	pipe2(2)         a kernel pipe: two fds, either of which can be handed to a child
//	posix_spawnp(3)  start a child; it inherits our fds 0/1/2, rearranged by file actions
//	waitpid(2)       wait for it and read the wait status
//	kill(2)          signal it
//
// So what this file has is a pid and file descriptors, which is what a Unix
// process has, and the code that uses it (exec_wasip1.go) does what os/exec
// does on Unix. Nothing here invents a new mechanism: adding missing syscalls as
// host functions is how wasip1 hosts extend the ABI (wasmedge does it for
// sockets), and the namespace is separate from wasi_snapshot_preview1 so that
// nothing pretends to be standard. The byte layout of the arguments is described
// in BrowserOS's README ("本物と同じ形で起こす").

//go:wasmimport browser_os_process pipe2
//go:noescape
func pipe2(fds unsafe.Pointer, flags uint32) uint32

//go:wasmimport browser_os_process posix_spawnp
//go:noescape
func posixSpawnp(
	pid unsafe.Pointer,
	file unsafe.Pointer, fileLen uint32,
	actions unsafe.Pointer, actionsLen uint32,
	attr unsafe.Pointer, attrLen uint32,
	argv unsafe.Pointer, argvLen uint32,
	envp unsafe.Pointer, envpLen uint32,
) uint32

//go:wasmimport browser_os_process waitpid
//go:noescape
func waitpid(pid uint32, status unsafe.Pointer, options uint32) uint32

//go:wasmimport browser_os_process kill
func killProcess(pid uint32, signal uint32) uint32

// The open flags of the guest's libc (wasi-libc's fcntl.h), which is what
// BrowserOS reads addopen's oflag as.
const (
	oRDONLY = 0x04000000
	oWRONLY = 0x10000000
)

// The file action kinds BrowserOS defines (SPAWN_FILE_ACTION_* in its
// process/wasi-spawn.ts). glibc keeps posix_spawn_file_actions_t opaque, so
// there is no layout to copy; the names are the POSIX function names.
const (
	actionOpen  = 1 // posix_spawn_file_actions_addopen
	actionClose = 2 // posix_spawn_file_actions_addclose
	actionDup2  = 3 // posix_spawn_file_actions_adddup2
	actionChdir = 4 // posix_spawn_file_actions_addchdir (POSIX.1-2024)
)

// CanSpawn reports whether this host can start child processes.
//
// It asks by trying, with nothing to run: a host that has processes rejects the
// empty argv (EINVAL), and one that does not answers the way an unimplemented
// syscall always has (ENOSYS). Neither starts anything.
func CanSpawn() bool {
	var pid uint32
	errno := posixSpawnp(unsafe.Pointer(&pid), nil, 0, nil, 0, nil, 0, nil, 0, nil, 0)
	return syscall.Errno(errno) != syscall.ENOSYS
}

// Pipe returns a kernel pipe: a read end and a write end, both plain fds.
func Pipe() (read, write int, err error) {
	var fds [2]int32
	if errno := pipe2(unsafe.Pointer(&fds[0]), 0); errno != 0 {
		return -1, -1, fmt.Errorf("browseros: pipe: %w", syscall.Errno(errno))
	}
	return int(fds[0]), int(fds[1]), nil
}

// FileActions is a posix_spawn_file_actions_t: what the child does with its
// file descriptors before it runs, in order.
type FileActions struct {
	bytes []byte
}

func (a *FileActions) add(op uint32, fd, newfd int, oflag uint32, path string) {
	var header [24]byte
	binary.LittleEndian.PutUint32(header[0:], op)
	binary.LittleEndian.PutUint32(header[4:], uint32(int32(fd)))
	binary.LittleEndian.PutUint32(header[8:], uint32(int32(newfd)))
	binary.LittleEndian.PutUint32(header[12:], oflag)
	binary.LittleEndian.PutUint32(header[20:], uint32(len(path)))
	a.bytes = append(a.bytes, header[:]...)
	a.bytes = append(a.bytes, path...)
	// The next action starts on a 4-byte boundary.
	for len(a.bytes)%4 != 0 {
		a.bytes = append(a.bytes, 0)
	}
}

// Dup2 makes the child's newfd refer to what our fd refers to.
func (a *FileActions) Dup2(fd, newfd int) { a.add(actionDup2, fd, newfd, 0, "") }

// Close closes the child's fd.
func (a *FileActions) Close(fd int) { a.add(actionClose, fd, 0, 0, "") }

// OpenNull opens /dev/null as the child's fd, for reading or for writing.
func (a *FileActions) OpenNull(fd int, write bool) {
	oflag := uint32(oRDONLY)
	if write {
		oflag = oWRONLY
	}
	a.add(actionOpen, fd, 0, oflag, "/dev/null")
}

// Chdir sets the child's working directory. The working directory belongs to
// the guest's libc (BrowserOS does not see our chdir), so the caller passes it.
func (a *FileActions) Chdir(path string) { a.add(actionChdir, 0, 0, 0, path) }

// Spawn starts file with argv and exactly env, as posix_spawnp(3) does: file
// is looked up on our PATH when it has no slash, and a file that cannot be
// found is an error (no process is created).
func Spawn(file string, argv, env []string, actions *FileActions) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("browseros: spawn needs a program to run")
	}
	// The lists cross as NUL-separated bytes, the way execve(2) and environ(7)
	// have always looked.
	fileBytes := []byte(file)
	argvBytes := nulSeparated(argv)
	envBytes := nulSeparated(env)
	var actionBytes []byte
	if actions != nil {
		actionBytes = actions.bytes
	}
	var pid uint32
	errno := posixSpawnp(
		unsafe.Pointer(&pid),
		pointerOf(fileBytes), uint32(len(fileBytes)),
		pointerOf(actionBytes), uint32(len(actionBytes)),
		nil, 0,
		pointerOf(argvBytes), uint32(len(argvBytes)),
		pointerOf(envBytes), uint32(len(envBytes)),
	)
	if errno != 0 {
		return -1, &startError{file: file, err: syscall.Errno(errno)}
	}
	return int(pid), nil
}

// wnohang is waitpid's WNOHANG (the value Linux uses, and BrowserOS reads).
const wnohang = 1

// waitInterval is how long Wait sleeps between asking whether the child is done.
const waitInterval = 5 * time.Millisecond

// Wait waits until the child exits and returns its wait status.
//
// **It asks with WNOHANG and sleeps in between, instead of blocking in waitpid.**
// wasip1 has no threads, so a blocking waitpid stops the whole module - including
// the goroutine that feeds the child's stdin, which the child may be waiting on
// before it can exit. time.Sleep parks only this goroutine and lets the others run.
func Wait(pid int) (WaitStatus, error) {
	for {
		var status uint32
		errno := syscall.Errno(waitpid(uint32(pid), unsafe.Pointer(&status), wnohang))
		if errno == 0 {
			return WaitStatus(status), nil
		}
		if errno != syscall.EAGAIN {
			return 0, fmt.Errorf("browseros: cannot wait for pid %d: %w", pid, errno)
		}
		time.Sleep(waitInterval)
	}
}

// Kill stops the child. It is not an error to kill a child that already exited.
func Kill(pid int) {
	_ = killProcess(uint32(pid), uint32(syscall.SIGKILL))
}

// WaitStatus is a POSIX wait status: how a child ended.
type WaitStatus uint32

// Signaled reports whether a signal stopped the child (WIFSIGNALED).
func (s WaitStatus) Signaled() bool { return s&0x7f != 0 && s&0x7f != 0x7f }

// Signal is the signal that stopped the child (WTERMSIG).
func (s WaitStatus) Signal() syscall.Signal { return syscall.Signal(s & 0x7f) }

// ExitCode is the exit code (WEXITSTATUS), or -1 if a signal stopped the child,
// which is what os.ProcessState.ExitCode reports.
func (s WaitStatus) ExitCode() int {
	if s.Signaled() {
		return -1
	}
	return int((s >> 8) & 0xff)
}

// startError is what Spawn returns when nothing could be started, shaped like
// the *fs.PathError os/exec gives for a missing program.
type startError struct {
	file string
	err  syscall.Errno
}

func (e *startError) Error() string {
	return fmt.Sprintf("browseros: cannot start %s: %v", e.file, e.err)
}
func (e *startError) Unwrap() error { return e.err }

// nulSeparated joins the entries the way execve(2) hands over argv and envp.
func nulSeparated(entries []string) []byte {
	if len(entries) == 0 {
		return nil
	}
	return []byte(strings.Join(entries, "\x00"))
}

func pointerOf(bytes []byte) unsafe.Pointer {
	if len(bytes) == 0 {
		return nil
	}
	return unsafe.Pointer(&bytes[0])
}
