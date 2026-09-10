//go:build wasip1

package browseros

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

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

// Run streams the child's output into whatever the caller attached to the
// command (gh points Stdout/Stderr at the terminal for `git push`, and at
// buffers elsewhere).
func (r *runnable) Run() error {
	_, err := r.run(r.cmd.Stdout, r.cmd.Stderr)
	return err
}

// Output collects standard output, the way exec.Cmd.Output does. Standard error
// is collected too and put into the error, since that is the text gh shows when
// git fails.
func (r *runnable) Output() ([]byte, error) {
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	_, err := r.run(stdout, stderr)
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			err = fmt.Errorf("%s: %w", message, err)
		}
		return []byte(stdout.String()), err
	}
	return []byte(stdout.String()), nil
}

func (r *runnable) run(stdout, stderr io.Writer) (int, error) {
	argv := r.cmd.Args
	if len(argv) == 0 {
		argv = []string{r.cmd.Path}
	}

	job, err := Spawn(argv, r.cmd.Dir, r.cmd.Env)
	if err != nil {
		return -1, err
	}
	defer job.Close()

	var wg sync.WaitGroup
	copyOut := func(dst io.Writer, src io.Reader) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if dst == nil {
				dst = io.Discard
			}
			_, _ = io.Copy(dst, src)
		}()
	}
	copyOut(stdout, job.Stdout)
	copyOut(stderr, job.Stderr)

	if input := childInput(r.cmd.Stdin); input != nil {
		// Not part of the wait group: the child is free to stop reading before the
		// input runs out, and this build must never wait on a write.
		go func() {
			defer job.Stdin.Close()
			_, _ = io.Copy(job.Stdin, input)
		}()
	} else {
		// The child sees EOF straight away, which is what exec.Cmd does with a nil
		// Stdin.
		_ = job.Stdin.Close()
	}

	code, err := job.Wait()
	// Only wait for the copies after the child is gone: the streams end when
	// the last buffered byte has been handed over, not when the process exits
	wg.Wait()
	if err != nil {
		return -1, err
	}
	if code != 0 {
		return code, &exitError{argv: argv, code: code}
	}
	return 0, nil
}

// childInput decides what to feed the child.
//
// **A file is never copied.** gh points a command's Stdin at its own standard
// input so that git can talk to the terminal, and on a normal platform that
// costs nothing: the child inherits the fd. Here it cannot - BrowserOS gives
// each process its own input channel - so copying would mean reading the
// terminal, and a read of a terminal nobody is typing at blocks forever. Since
// wasip1 has no threads, that one read stops the whole module, gh included.
//
// Everything else (the in-memory readers gh passes with git.WithStdin, which is
// how a command is actually given input) is copied as usual.
func childInput(stdin io.Reader) io.Reader {
	if stdin == nil {
		return nil
	}
	if _, isFile := stdin.(*os.File); isFile {
		return nil
	}
	return stdin
}

// exitError is what a failed command returns. gh looks for the ExitCode method
// (git/command.go), which is also how its own tests report exit codes - there is
// no *exec.ExitError to hand out, because no process was ever waited on.
type exitError struct {
	argv []string
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("%s: exit status %d", e.argv[0], e.code)
}

func (e *exitError) ExitCode() int {
	return e.code
}

// LookPathHint says what to use as the program name. BrowserOS resolves names
// against PATH when it starts a process (as execvp(3) does), so gh does not
// need to find git on disk first - and it could not, since the executables a
// BrowserOS carries are entries in the kernel's table rather than files with an
// executable bit.
func LookPathHint(name string) (string, error) {
	if !CanSpawn() {
		return "", fmt.Errorf("BrowserOS was started without child processes, so %s cannot be run", name)
	}
	return name, nil
}
