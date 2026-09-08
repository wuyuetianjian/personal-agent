//go:build !windows

package codingagent

import (
    "os/exec"
    "syscall"
    "time"
)

func configureProcess(cmd *exec.Cmd) {
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,
    }
}

func terminateProcessTree(pid int, grace time.Duration) {
    _ = syscall.Kill(-pid, syscall.SIGTERM)
    time.Sleep(grace)
    _ = syscall.Kill(-pid, syscall.SIGKILL)
}
