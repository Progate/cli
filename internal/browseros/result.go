package browseros

// resultOf reads a system call's result out of what browser_os_linux.syscall
// returns.
//
// The import returns an i64, but a riscv32 Linux system call returns a 32-bit
// long, so BrowserOS puts the result in the low 32 bits and keeps the high 32
// bits for itself (it flags queued signals there for a libc that runs signal
// handlers on the way out of a system call). The low half is not sign-extended:
// -ENOENT arrives as 0xfffffffe. Reading the i64 as the result would take every
// failure for a success - execvp(3)'s walk over PATH, for one, would stop at
// the first directory without the program instead of trying the next.
//
// The flag in the high half is ignored. gh registers no signal handlers with
// BrowserOS, so a queued signal has nothing to run here; the kernel carries out
// the default action of a signal itself.
//
// This file has no build tag so that the decoding is tested on the host.
//
//go:nosplit
func resultOf(raw int64) int64 {
	return int64(int32(raw))
}
