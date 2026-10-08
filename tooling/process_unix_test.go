//go:build darwin || linux

package tooling_test

import (
	"os/exec"
	"syscall"
)

// Subprocesses created by integration fixtures must not outlive a failed test.
func isolateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
