//go:build wasip1

package browseros

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
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
// BrowserOS supplies the three calls preview1 is missing as host functions, and
// they are the POSIX ones:
//
//	posix_spawn(3)  start a child; its stdin/stdout/stderr arrive as pipe fds
//	waitpid(2)      wait for it and read the exit status
//	kill(2)         signal it
//
// So what this file has is a pid and three fds, which is what a Unix process
// has. Nothing here invents a new mechanism: adding missing syscalls as host
// functions is how wasip1 hosts extend the ABI (wasmedge does it for sockets),
// and the namespace is separate from wasi_snapshot_preview1 so that nothing
// pretends to be standard.

//go:wasmimport browser_os_process posix_spawn
//go:noescape
func posixSpawn(
	argv unsafe.Pointer, argvLen uint32,
	env unsafe.Pointer, envLen uint32,
	cwd unsafe.Pointer, cwdLen uint32,
	fds unsafe.Pointer,
	pid unsafe.Pointer,
) uint32

//go:wasmimport browser_os_process waitpid
//go:noescape
func waitpid(pid uint32, status unsafe.Pointer, options uint32) uint32

//go:wasmimport browser_os_process kill
func killProcess(pid uint32, signal uint32) uint32

// CanSpawn reports whether this host can start child processes.
//
// It asks by trying, with nothing to run: a host that has processes rejects the
// empty argv (EINVAL), and one that does not answers the way an unimplemented
// syscall always has (ENOSYS). Neither starts anything.
func CanSpawn() bool {
	var fds [3]uint32
	var pid uint32
	errno := posixSpawn(nil, 0, nil, 0, nil, 0, unsafe.Pointer(&fds[0]), unsafe.Pointer(&pid))
	return syscall.Errno(errno) != syscall.ENOSYS
}

// Job is a running child process: a pid and its three pipes.
type Job struct {
	pid    uint32
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

// Spawn starts a child. dir and env may be empty, in which case the child
// inherits them. env is in the KEY=VALUE form os/exec uses; BrowserOS merges it
// into the inherited environment rather than replacing it, so a child never
// loses PATH.
func Spawn(argv []string, dir string, env []string) (*Job, error) {
	if len(argv) == 0 {
		return nil, errors.New("browseros: spawn needs a program to run")
	}

	// The three lists cross as NUL-separated bytes, the way execve(2) and
	// environ(7) have always looked.
	argvBytes := nulSeparated(argv)
	envBytes := nulSeparated(env)
	dirBytes := []byte(dir)

	var fds [3]uint32
	var pid uint32
	errno := posixSpawn(
		pointerOf(argvBytes), uint32(len(argvBytes)),
		pointerOf(envBytes), uint32(len(envBytes)),
		pointerOf(dirBytes), uint32(len(dirBytes)),
		unsafe.Pointer(&fds[0]),
		unsafe.Pointer(&pid),
	)
	if errno != 0 {
		return nil, fmt.Errorf("browseros: cannot start %s: %w", argv[0], syscall.Errno(errno))
	}

	job := &Job{pid: pid}
	var err error
	if job.Stdin, err = pipe(fds[0], "stdin"); err != nil {
		return nil, err
	}
	if job.Stdout, err = pipe(fds[1], "stdout"); err != nil {
		return nil, err
	}
	if job.Stderr, err = pipe(fds[2], "stderr"); err != nil {
		return nil, err
	}
	return job, nil
}

// pipe wraps one of the child's fds.
//
// **Non-blocking, always.** These are pipes, so a read of one waits for the
// child - and wasip1 has no threads, so a waiting read stops the whole module,
// including the goroutine that was going to read the other pipe. Marking the fd
// non-blocking (what a Unix program does with O_NONBLOCK) makes Go register it
// with the poller, which parks only the goroutine that is waiting.
func pipe(fd uint32, name string) (*os.File, error) {
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		return nil, &os.PathError{Op: "setnonblock", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Wait blocks until the child exits and returns its exit code.
func (j *Job) Wait() (int, error) {
	var status uint32
	if errno := waitpid(j.pid, unsafe.Pointer(&status), 0); errno != 0 {
		return -1, fmt.Errorf("browseros: cannot wait for pid %d: %w", j.pid, syscall.Errno(errno))
	}
	// WEXITSTATUS(status). The low byte says how it ended; zero means normally.
	return int((status >> 8) & 0xff), nil
}

// Kill stops the child. It is not an error to kill a child that already exited.
func (j *Job) Kill() {
	_ = killProcess(j.pid, uint32(syscall.SIGKILL))
}

// Close releases the pipes. The child is not killed: waiting for it is the
// caller's job, exactly as it is with os/exec.
func (j *Job) Close() {
	for _, file := range []*os.File{j.Stdin, j.Stdout, j.Stderr} {
		if file != nil {
			_ = file.Close()
		}
	}
}

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
