//go:build wasip1

package browseros

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
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
// BrowserOS takes the Linux system calls through one import,
// browser_os_linux.syscall(nr, a0..a5) -> i64, with the numbers, arguments and
// errno values of riscv32 Linux (asm-generic). Starting a child is what Go's
// os/exec does on Linux:
//
//	pipe2(2)   a kernel pipe: two fds, either of which can be handed to a child
//	clone(2)   CLONE_VM|CLONE_VFORK: the child borrows our memory until it execs
//	dup3(2), close(2), openat(2), chdir(2)   the child arranges its fds and directory
//	execve(2)  the child becomes the program
//	waitid(2)  wait for it and read how it ended
//	kill(2)    signal it
//
// A wasm module cannot run two flows of execution on one stack, so BrowserOS
// runs the child's part by calling back into this module: during clone(2) the
// kernel calls the export __browseros_clone_child_return, which does the
// child's work and returns. Inside it, a successful execve(2) or exit(2) returns
// 0 (on Linux they do not return at all), which is how the child knows its part
// is over. clone(2) then returns the child's pid to us, as it does on Linux.

// syscall(nr, a0..a5). The answer is in the low 32 bits of the i64 (see
// resultOf); the high 32 bits are the kernel's to use.
//
//go:wasmimport browser_os_linux syscall
func rawLinuxSyscall(nr int32, a0, a1, a2, a3, a4, a5 int64) int64

// linuxSyscall makes a system call and returns its result: non-negative on
// success, -errno (the Linux value) on failure.
//
// It runs in the child's part of clone(2) too, so it must not grow the stack.
//
//go:nosplit
func linuxSyscall(nr int32, a0, a1, a2, a3, a4, a5 int64) int64 {
	return resultOf(rawLinuxSyscall(nr, a0, a1, a2, a3, a4, a5))
}

// The child's part of clone(2). BrowserOS calls it while clone(2) runs; it must
// not be called otherwise.
//
//go:wasmexport __browseros_clone_child_return
func cloneChildEntry() { runChild() }

// System call numbers (asm-generic Linux).
const (
	sysDup3    = 24
	sysFcntl64 = 25
	sysChdir   = 49
	sysOpenat  = 56
	sysClose   = 57
	sysPipe2   = 59
	sysExit    = 93
	sysWaitid  = 95
	sysKill    = 129
	sysClone   = 220
	sysExecve  = 221
)

// Flags, in Linux's values.
const (
	cloneVM      = 0x100
	cloneSighand = 0x800
	cloneVfork   = 0x4000
	sigchld      = 17
	atFdcwd      = -100
	oRDONLY      = 0
	oWRONLY      = 1
	fGetfd       = 1
	fSetfd       = 2
	fdCloexec    = 1
	pPID         = 1 // waitid's P_PID
	wNOHANG      = 1
	wEXITED      = 4
)

// siginfo_t as waitid(2) fills it in (riscv32 Linux).
const (
	siginfoBytes  = 128
	siginfoCode   = 8
	siginfoPid    = 12
	siginfoStatus = 20
	cldKilled     = 2
	cldDumped     = 3
)

// The Linux errno values, mapped to the wasip1 syscall.Errno of the same name
// (Go's wasip1 numbers errors as WASI does).
var errnoOfLinux = map[int64]syscall.Errno{
	7: syscall.E2BIG, 13: syscall.EACCES, 98: syscall.EADDRINUSE, 99: syscall.EADDRNOTAVAIL,
	97: syscall.EAFNOSUPPORT, 11: syscall.EAGAIN, 114: syscall.EALREADY, 9: syscall.EBADF,
	74: syscall.EBADMSG, 16: syscall.EBUSY, 125: syscall.ECANCELED, 10: syscall.ECHILD,
	103: syscall.ECONNABORTED, 111: syscall.ECONNREFUSED, 104: syscall.ECONNRESET, 35: syscall.EDEADLK,
	89: syscall.EDESTADDRREQ, 33: syscall.EDOM, 122: syscall.EDQUOT, 17: syscall.EEXIST,
	14: syscall.EFAULT, 27: syscall.EFBIG, 113: syscall.EHOSTUNREACH, 43: syscall.EIDRM,
	84: syscall.EILSEQ, 115: syscall.EINPROGRESS, 4: syscall.EINTR, 22: syscall.EINVAL,
	5: syscall.EIO, 106: syscall.EISCONN, 21: syscall.EISDIR, 40: syscall.ELOOP,
	24: syscall.EMFILE, 31: syscall.EMLINK, 90: syscall.EMSGSIZE, 72: syscall.EMULTIHOP,
	36: syscall.ENAMETOOLONG, 100: syscall.ENETDOWN, 102: syscall.ENETRESET, 101: syscall.ENETUNREACH,
	23: syscall.ENFILE, 105: syscall.ENOBUFS, 19: syscall.ENODEV, 2: syscall.ENOENT,
	8: syscall.ENOEXEC, 37: syscall.ENOLCK, 67: syscall.ENOLINK, 12: syscall.ENOMEM,
	42: syscall.ENOMSG, 92: syscall.ENOPROTOOPT, 28: syscall.ENOSPC, 38: syscall.ENOSYS,
	107: syscall.ENOTCONN, 20: syscall.ENOTDIR, 39: syscall.ENOTEMPTY, 131: syscall.ENOTRECOVERABLE,
	88: syscall.ENOTSOCK, 95: syscall.ENOTSUP, 25: syscall.ENOTTY, 6: syscall.ENXIO,
	75: syscall.EOVERFLOW, 130: syscall.EOWNERDEAD, 1: syscall.EPERM, 32: syscall.EPIPE,
	71: syscall.EPROTO, 93: syscall.EPROTONOSUPPORT, 91: syscall.EPROTOTYPE, 34: syscall.ERANGE,
	30: syscall.EROFS, 29: syscall.ESPIPE, 3: syscall.ESRCH, 116: syscall.ESTALE,
	110: syscall.ETIMEDOUT, 26: syscall.ETXTBSY, 18: syscall.EXDEV,
}

// errnoOf turns a system call's result into an errno (0 for success, EIO for a
// value wasip1 has no name for).
func errnoOf(r int64) syscall.Errno {
	if r >= 0 {
		return 0
	}
	if errno, ok := errnoOfLinux[-r]; ok {
		return errno
	}
	return syscall.EIO
}

// ptrArg passes a linear-memory address as a system call argument. It is taken in
// the call expression itself and does not grow the stack, so the goroutine stack
// cannot move in between.
//
//go:nosplit
func ptrArg(p unsafe.Pointer) int64 { return int64(uintptr(p)) }

// CanSpawn reports whether this host can start child processes.
//
// It asks by trying with flags clone(2) rejects: a kernel that has clone
// answers EINVAL (CLONE_SIGHAND needs CLONE_VM), one that does not answers
// ENOSYS. Neither starts anything.
func CanSpawn() bool {
	return errnoOf(linuxSyscall(sysClone, cloneSighand|sigchld, 0, 0, 0, 0, 0)) != syscall.ENOSYS
}

// Pipe returns a kernel pipe: a read end and a write end, both plain fds.
func Pipe() (read, write int, err error) {
	var fds [2]int32
	if errno := errnoOf(linuxSyscall(sysPipe2, ptrArg(unsafe.Pointer(&fds)), 0, 0, 0, 0, 0)); errno != 0 {
		return -1, -1, fmt.Errorf("browseros: pipe: %w", errno)
	}
	return int(fds[0]), int(fds[1]), nil
}

// The kinds of file action, named after the posix_spawn_file_actions_add* functions.
const (
	actionOpen = iota + 1
	actionClose
	actionDup2
	actionChdir
)

// fileAction is one entry of FileActions.
type fileAction struct {
	op    int
	fd    int32
	newfd int32
	oflag int64
	path  []byte // NUL-terminated
}

// FileActions is a posix_spawn_file_actions_t: what the child does with its
// file descriptors before it runs, in order.
type FileActions struct {
	actions []fileAction
}

// Dup2 makes the child's newfd refer to what our fd refers to.
func (a *FileActions) Dup2(fd, newfd int) {
	a.actions = append(a.actions, fileAction{op: actionDup2, fd: int32(fd), newfd: int32(newfd)})
}

// Close closes the child's fd.
func (a *FileActions) Close(fd int) {
	a.actions = append(a.actions, fileAction{op: actionClose, fd: int32(fd)})
}

// OpenNull opens /dev/null as the child's fd, for reading or for writing.
func (a *FileActions) OpenNull(fd int, write bool) {
	oflag := int64(oRDONLY)
	if write {
		oflag = oWRONLY
	}
	a.actions = append(a.actions, fileAction{op: actionOpen, fd: int32(fd), oflag: oflag, path: []byte("/dev/null\x00")})
}

// Chdir sets the child's working directory. Go keeps its working directory to
// itself on wasip1, so the caller passes it.
func (a *FileActions) Chdir(path string) {
	a.actions = append(a.actions, fileAction{op: actionChdir, path: append([]byte(path), 0)})
}

// cloneChild is what the child's part reads. Everything is prepared before
// clone(2), so the child does not allocate. errno receives the Linux errno of a
// failure in the child's part.
var cloneChild struct {
	candidates [][]byte // NUL-terminated paths to try, in order
	argv       []uint32
	envp       []uint32
	actions    []fileAction
	errno      int64
}

// spawnMu lets one Spawn at a time use cloneChild.
var spawnMu sync.Mutex

// Spawn starts file with argv and exactly env, as posix_spawnp(3) does: file
// is looked up on our PATH when it has no slash, and a file that cannot be
// found is an error (no process is created).
func Spawn(file string, argv, env []string, actions *FileActions) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("browseros: spawn needs a program to run")
	}
	candidates, err := candidatesOf(file)
	if err != nil {
		return -1, &startError{file: file, err: syscall.EINVAL}
	}
	argvPtrs, argvBufs, err := cStrings(argv)
	if err != nil {
		return -1, &startError{file: file, err: syscall.EINVAL}
	}
	envPtrs, envBufs, err := cStrings(env)
	if err != nil {
		return -1, &startError{file: file, err: syscall.EINVAL}
	}
	var list []fileAction
	if actions != nil {
		list = actions.actions
	}

	spawnMu.Lock()
	defer spawnMu.Unlock()
	cloneChild.candidates, cloneChild.argv, cloneChild.envp, cloneChild.actions, cloneChild.errno = candidates, argvPtrs, envPtrs, list, 0
	r := linuxSyscall(sysClone, cloneVM|cloneVfork|sigchld, 0, 0, 0, 0, 0)
	childErrno := cloneChild.errno
	cloneChild.candidates, cloneChild.argv, cloneChild.envp, cloneChild.actions = nil, nil, nil, nil
	runtime.KeepAlive(argvBufs)
	runtime.KeepAlive(envBufs)
	if errno := errnoOf(r); errno != 0 {
		return -1, &startError{file: file, err: errno}
	}
	pid := int(r)
	if childErrno != 0 {
		// The child ended with 127 before it became the program; reap it.
		_, _ = Wait(pid)
		return -1, &startError{file: file, err: errnoOf(-childErrno)}
	}
	return pid, nil
}

// runChild is the child's part: the file actions, then execve(2) of each
// candidate in turn (execvp(3): skip a missing one, remember a forbidden one).
// When the part is over (execve succeeded or exit was called) the call returns 0.
func runChild() {
	c := &cloneChild
	if c.argv == nil {
		return
	}
	for _, action := range c.actions {
		var r int64
		switch action.op {
		case actionOpen:
			r = linuxSyscall(sysOpenat, atFdcwd, int64(uintptr(unsafe.Pointer(&action.path[0]))), action.oflag, 0, 0, 0)
			if r >= 0 && r != int64(action.fd) {
				opened := r
				r = linuxSyscall(sysDup3, opened, int64(action.fd), 0, 0, 0, 0)
				linuxSyscall(sysClose, opened, 0, 0, 0, 0, 0)
			}
		case actionClose:
			// Closing an fd that is not open is not a failure here, as in musl.
			linuxSyscall(sysClose, int64(action.fd), 0, 0, 0, 0, 0)
		case actionDup2:
			if action.fd != action.newfd {
				r = linuxSyscall(sysDup3, int64(action.fd), int64(action.newfd), 0, 0, 0, 0)
			} else if r = linuxSyscall(sysFcntl64, int64(action.fd), fGetfd, 0, 0, 0, 0); r >= 0 {
				// dup2 onto itself keeps the fd across exec (POSIX.1-2024).
				r = linuxSyscall(sysFcntl64, int64(action.fd), fSetfd, r&^fdCloexec, 0, 0, 0)
			}
		case actionChdir:
			r = linuxSyscall(sysChdir, int64(uintptr(unsafe.Pointer(&action.path[0]))), 0, 0, 0, 0, 0)
		}
		if r < 0 {
			c.errno = -r
			linuxSyscall(sysExit, 127, 0, 0, 0, 0, 0)
			return
		}
	}
	const eNOENT, eNOTDIR, eACCES = 2, 20, 13
	failure := int64(eNOENT)
candidates:
	for _, path := range c.candidates {
		r := linuxSyscall(sysExecve, int64(uintptr(unsafe.Pointer(&path[0]))), int64(uintptr(unsafe.Pointer(&c.argv[0]))), int64(uintptr(unsafe.Pointer(&c.envp[0]))), 0, 0, 0)
		if r == 0 {
			return
		}
		switch -r {
		case eNOENT, eNOTDIR:
			continue
		case eACCES:
			failure = eACCES
			continue
		}
		failure = -r
		break candidates
	}
	c.errno = failure
	linuxSyscall(sysExit, 127, 0, 0, 0, 0, 0)
}

// defaultPath is where a program is looked up when PATH is not set.
const defaultPath = "/usr/local/bin:/usr/bin:/bin"

// candidatesOf lists the paths execvp(3) would try for file, from our PATH.
func candidatesOf(file string) ([][]byte, error) {
	if strings.Contains(file, "/") {
		path, err := syscall.ByteSliceFromString(file)
		return [][]byte{path}, err
	}
	search, ok := os.LookupEnv("PATH")
	if !ok {
		search = defaultPath
	}
	var candidates [][]byte
	for _, dir := range strings.Split(search, ":") {
		if dir == "" {
			dir = "."
		}
		path, err := syscall.ByteSliceFromString(strings.TrimSuffix(dir, "/") + "/" + file)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, path)
	}
	return candidates, nil
}

// cStrings makes a NULL-terminated array of char*. The second result holds the
// strings and must stay alive until the call is over.
func cStrings(items []string) ([]uint32, [][]byte, error) {
	ptrs := make([]uint32, 0, len(items)+1)
	bufs := make([][]byte, 0, len(items))
	for _, s := range items {
		b, err := syscall.ByteSliceFromString(s)
		if err != nil {
			return nil, nil, err
		}
		bufs = append(bufs, b)
		ptrs = append(ptrs, uint32(uintptr(unsafe.Pointer(&b[0]))))
	}
	return append(ptrs, 0), bufs, nil
}

// waitInterval is how long Wait sleeps between asking whether the child is done.
const waitInterval = 5 * time.Millisecond

// Wait waits until the child exits and returns its wait status.
//
// **It asks with WNOHANG and sleeps in between, instead of blocking in waitid.**
// wasip1 has no threads, so a blocking waitid stops the whole module - including
// the goroutine that feeds the child's stdin, which the child may be waiting on
// before it can exit. time.Sleep parks only this goroutine and lets the others run.
func Wait(pid int) (WaitStatus, error) {
	for {
		var info [siginfoBytes]byte
		errno := errnoOf(linuxSyscall(sysWaitid, pPID, int64(pid), ptrArg(unsafe.Pointer(&info)), wEXITED|wNOHANG, 0, 0))
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, fmt.Errorf("browseros: cannot wait for pid %d: %w", pid, errno)
		}
		// With WNOHANG, a child that has not ended leaves si_pid at 0.
		if binary.LittleEndian.Uint32(info[siginfoPid:]) != 0 {
			status := binary.LittleEndian.Uint32(info[siginfoStatus:])
			switch int32(binary.LittleEndian.Uint32(info[siginfoCode:])) {
			case cldKilled, cldDumped:
				return WaitStatus(status & 0x7f), nil
			}
			return WaitStatus((status & 0xff) << 8), nil
		}
		time.Sleep(waitInterval)
	}
}

// Kill stops the child. It is not an error to kill a child that already exited.
func Kill(pid int) {
	_ = linuxSyscall(sysKill, int64(pid), 9, 0, 0, 0, 0)
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
