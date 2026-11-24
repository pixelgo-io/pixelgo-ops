//go:build !windows

package proxy

// Sending a signal to another process, on the platforms that have signals.
//
// This file exists because of the cross-build, not in spite of it. The proxy
// subsystem is Unix in substance - it runs tinyproxy and it is enforced by
// nftables - but cmd/pixelgo-ops imports it on every platform, so one call to
// syscall.Kill was enough to stop the Windows build of a package that has
// nothing to do with Windows. The hypervisor backend behind //go:build windows
// is the only reason that build is made at all, and it was the thing left
// unverified.
//
// The split is two functions rather than a build tag around the callers. The
// logic in Stop and reload - the grace period, the escalation, the reload being
// a request and not a guarantee - is the part worth getting right, and it is
// the same reasoning on any system that has a signal to send. Only the system
// call differs, so only the system call is here.

import "syscall"

// signalPid sends sig to pid.
func signalPid(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// processAlive reports whether pid exists.
//
// Signal 0 checks for existence without delivering anything. It answers for
// any process the caller may signal, which is what the question means here:
// the pid came out of our own PID file.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
