//go:build windows

package util

import (
	"os/exec"
	"syscall"
)

// SetProcessGroup is a no-op on Windows.
// Process group management is not supported on Windows.
func SetProcessGroup(cmd *exec.Cmd) {}

// EnablePDEathSignal is a no-op on Windows.
func EnablePDEathSignal(sig syscall.Signal) bool {
	return false
}
