//go:build !windows && !darwin

package util

import (
	"os/exec"
	"syscall"
)

const (
	prSetPDEathsig = 1
)

// SetProcessGroup configures a command to run in its own process group so that
// context cancellation kills the entire process tree, preventing orphaned children.
// On Linux, also sets PR_SET_PDEATHSIG to kill the process when its parent dies.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

// EnablePDEathSignal enables PR_SET_PDEATHSIG for the current process.
// This causes the process to receive a signal when its parent dies.
// On Linux, uses prctl(PR_SET_PDEATHSIG, signal). On other Unix systems, this is a no-op.
// Returns true on Linux, false on other systems.
func EnablePDEathSignal(sig syscall.Signal) bool {
	return setPdeathsig(sig)
}

func setPdeathsig(sig syscall.Signal) bool {
	// SYS_prctl = 157 on Linux
	const sysPrctl = 157
	_, _, errno := syscall.Syscall6(sysPrctl, prSetPDEathsig, uintptr(sig), 0, 0, 0, 0)
	return errno == 0
}
