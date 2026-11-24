//go:build windows

package proxy

// The same two functions, for a platform that has no signals.
//
// Nothing here is a port of the proxy. tinyproxy and nftables are not Windows
// software, Available refuses before any of this is reached, and the Windows
// build exists only so that the Hyper-V backend compiles. The point of this
// file is that the failure is a refusal with a reason rather than a build
// error in a package the person never asked to use.
//
// Returning an error is deliberate, and better than a silent no-op: a Stop that
// reported success without stopping anything would be the worse outcome of the
// two, and the one that is hard to notice.

import (
	"errors"
	"os"
	"syscall"
)

// ErrNoSignals is returned by every operation here. It is not a missing
// feature so much as a missing concept: there is no Windows equivalent of
// asking tinyproxy to re-read its configuration.
var ErrNoSignals = errors.New("the proxy cannot be signalled on windows")

func signalPid(pid int, sig syscall.Signal) error {
	_ = pid
	_ = sig
	return ErrNoSignals
}

// processAlive reports whether pid exists.
//
// os.FindProcess actually opens the process on Windows, unlike on Unix where it
// never fails, so a failure here is a real answer rather than an artefact of
// the API.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
