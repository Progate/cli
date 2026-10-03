//go:build wasip1

package browseros

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/cli/cli/v2/internal/run"
)

// InstallExec makes gh's "run this command" hook go through BrowserOS.
//
// gh runs git through run.PrepareCmd - a hook the tests already use to stub out
// execution. On wasip1 the same hook is the whole port: os/exec compiles but
// cannot start anything (no fork, no exec), so without this every command that
// touches the local repository fails with ENOSYS. With it, git runs as a real
// BrowserOS process (see spawn_wasip1.go), which in a browser means the wasm
// build of git next door.
//
// Only the two methods gh asks for are implemented, because run.Runnable is
// those two methods. Anything that reached for *exec.Cmd directly would still
// fail, and that is the honest outcome: it would be running a program this
// build cannot start.
func InstallExec() {
	run.PrepareCmd = func(cmd *exec.Cmd) run.Runnable {
		return &runnable{cmd: cmd}
	}
}

type runnable struct {
	cmd *exec.Cmd
}

// Run connects the child to whatever the caller attached to the command (gh
// points Stdin/Stdout/Stderr at the terminal for `git push`, and at buffers
// elsewhere).
func (r *runnable) Run() error {
	_, err := r.run(r.cmd.Stdin, r.cmd.Stdout, r.cmd.Stderr)
	return err
}

// Output collects standard output, the way exec.Cmd.Output does. Standard error
// is collected too and put into the error, since that is the text gh shows when
// git fails.
func (r *runnable) Output() ([]byte, error) {
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	_, err := r.run(r.cmd.Stdin, stdout, stderr)
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			err = fmt.Errorf("%s: %w", message, err)
		}
		return []byte(stdout.String()), err
	}
	return []byte(stdout.String()), nil
}

// The child's fds are first copied here and then moved into place, so that one
// dup2 cannot clobber the source of the next (stdout=os.Stderr with
// stderr=os.Stdout would otherwise end up as both pointing at the same place).
const scratchFd = 100

// run starts the child the way os/exec does on Unix:
//
//	nil        the child's fd is /dev/null
//	*os.File   the child gets that fd itself (the terminal, a file)
//	otherwise  a pipe, and a goroutine copies between it and the Reader/Writer
//
// So `git push` writes to the terminal directly and can read a credential
// prompt from it, while git.Output() captures through pipes.
func (r *runnable) run(stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	argv := r.cmd.Args
	if len(argv) == 0 {
		argv = []string{r.cmd.Path}
	}
	file := r.cmd.Path
	if file == "" {
		file = argv[0]
	}
	// exec.Cmd: a nil Env means the child inherits ours. posix_spawnp's envp is
	// the child's whole environment, so pass ours explicitly.
	env := r.cmd.Env
	if env == nil {
		env = os.Environ()
	}

	var (
		childFds   [3]int // our fds that become the child's 0, 1 and 2 (-1 for /dev/null)
		childEnds  []int  // pipe ends that belong to the child, closed here once it has started
		parentEnds []*os.File
		copies     sync.WaitGroup
		feed       func()
	)
	closeAll := func() {
		for _, fd := range childEnds {
			_ = syscall.Close(fd)
		}
		for _, file := range parentEnds {
			_ = file.Close()
		}
	}

	switch input := stdin.(type) {
	case nil:
		childFds[0] = -1
	case *os.File:
		childFds[0] = int(input.Fd())
	default:
		read, write, err := Pipe()
		if err != nil {
			return -1, err
		}
		childFds[0] = read
		childEnds = append(childEnds, read)
		writer, err := parentEnd(write, "stdin")
		if err != nil {
			_ = syscall.Close(read)
			return -1, err
		}
		parentEnds = append(parentEnds, writer)
		// Not part of the wait group: the child is free to stop reading before the
		// input runs out (the write then fails with EPIPE).
		feed = func() {
			go func() {
				defer writer.Close()
				_, _ = io.Copy(writer, input)
			}()
		}
	}

	output := func(target io.Writer, index int, name string) error {
		switch out := target.(type) {
		case nil:
			childFds[index] = -1
			return nil
		case *os.File:
			childFds[index] = int(out.Fd())
			return nil
		}
		// os/exec gives stderr the same pipe when it is the same writer as stdout,
		// so the two arrive in the order they were written.
		if index == 2 && target == stdout {
			childFds[2] = childFds[1]
			return nil
		}
		read, write, err := Pipe()
		if err != nil {
			return err
		}
		childFds[index] = write
		childEnds = append(childEnds, write)
		reader, err := parentEnd(read, name)
		if err != nil {
			_ = syscall.Close(read)
			return err
		}
		parentEnds = append(parentEnds, reader)
		copies.Add(1)
		go func() {
			defer copies.Done()
			_, _ = io.Copy(target, reader)
		}()
		return nil
	}
	if err := output(stdout, 1, "stdout"); err != nil {
		closeAll()
		return -1, err
	}
	if err := output(stderr, 2, "stderr"); err != nil {
		closeAll()
		return -1, err
	}

	actions := &FileActions{}
	for target, source := range childFds {
		if source >= 0 && source != target {
			actions.Dup2(source, scratchFd+target)
		}
	}
	for target, source := range childFds {
		switch {
		case source < 0:
			actions.OpenNull(target, target != 0)
		case source != target:
			actions.Dup2(scratchFd+target, target)
			actions.Close(scratchFd + target)
		}
	}
	dir := r.cmd.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if dir != "" {
		actions.Chdir(dir)
	}

	pid, err := Spawn(file, argv, env, actions)
	// The child has its own copies now (or never started); ours would keep the
	// pipes open and the readers would never see EOF.
	for _, fd := range childEnds {
		_ = syscall.Close(fd)
	}
	if err != nil {
		for _, file := range parentEnds {
			_ = file.Close()
		}
		return -1, err
	}
	if feed != nil {
		feed()
	}

	status, err := Wait(pid)
	// Only wait for the copies after the child is gone: the streams end when
	// the last buffered byte has been handed over, not when the process exits.
	copies.Wait()
	for _, file := range parentEnds {
		_ = file.Close()
	}
	if err != nil {
		return -1, err
	}
	if code := status.ExitCode(); code != 0 {
		return code, &exitError{argv: argv, status: status}
	}
	return 0, nil
}

// parentEnd wraps our end of a pipe.
//
// **Non-blocking, always.** A read of a pipe waits for the child - and wasip1
// has no threads, so a waiting read stops the whole module, including the
// goroutine that was going to read the other pipe. Marking the fd non-blocking
// (what a Unix program does with O_NONBLOCK) makes Go register it with the
// poller, which parks only the goroutine that is waiting.
func parentEnd(fd int, name string) (*os.File, error) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, &os.PathError{Op: "setnonblock", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// exitError is what a failed command returns. gh looks for the ExitCode method
// (git/command.go), which is also how its own tests report exit codes - there is
// no *exec.ExitError to hand out, because os/exec never waited on a process.
type exitError struct {
	argv   []string
	status WaitStatus
}

func (e *exitError) Error() string {
	if e.status.Signaled() {
		return fmt.Sprintf("%s: signal: %v", e.argv[0], e.status.Signal())
	}
	return fmt.Sprintf("%s: exit status %d", e.argv[0], e.status.ExitCode())
}

// ExitCode is -1 for a child stopped by a signal, as os.ProcessState reports.
func (e *exitError) ExitCode() int {
	return e.status.ExitCode()
}

// LookPathHint says what to use as the program name. posix_spawnp resolves
// names against PATH when it starts a process (as execvp(3) does), so gh does
// not need to find git on disk first - and it could not, since the executables
// a BrowserOS carries are entries in the kernel's table rather than files with
// an executable bit.
func LookPathHint(name string) (string, error) {
	if !CanSpawn() {
		return "", fmt.Errorf("BrowserOS was started without child processes, so %s cannot be run", name)
	}
	return name, nil
}
