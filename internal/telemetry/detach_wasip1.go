//go:build wasip1

package telemetry

import "syscall"

// detachAttrs returns nil because wasip1 has no processes to detach from: there
// is no fork, no exec and no process group. Nothing ever reads these attributes
// in a WebAssembly build, since the command that would spawn the detached child
// cannot start one.
func detachAttrs() *syscall.SysProcAttr {
	return nil
}
