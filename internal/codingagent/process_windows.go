//go:build windows

package codingagent

import (
    "os"
    "os/exec"
    "strconv"
    "time"
)

func configureProcess(cmd *exec.Cmd) {
    // Windows 不使用 Unix process group。
}

func terminateProcessTree(pid int, grace time.Duration) {
    // taskkill /T 会连同整个子进程树一起终止。
    // 对 Codex / Claude CLI 这种会继续拉子进程的程序很重要。
    cmd := exec.Command(
        "taskkill",
        "/PID", strconv.Itoa(pid),
        "/T",
        "/F",
    )

    if err := cmd.Run(); err == nil {
        return
    }

    // fallback
    if process, err := os.FindProcess(pid); err == nil {
        _ = process.Kill()
    }
}
