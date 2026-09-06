package release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Check struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

func (r Report) Failed() bool {
	for _, check := range r.Checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}

type Options struct {
	RepoRoot string
	Quick    bool
}

func RunChecks(ctx context.Context, opts Options) (Report, error) {
	root := opts.RepoRoot
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return Report{}, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	report.add(checkRequiredFiles(root)...)
	report.add(runCommand(ctx, root, "go test ./...", "go", "test", "./..."))
	report.add(runCommand(ctx, root, "go vet ./...", "go", "vet", "./..."))
	report.add(runCommand(ctx, root, "make build", "make", "build"))
	if !opts.Quick {
		report.add(runCommand(ctx, root, "go test -race ./...", "go", "test", "-race", "./..."))
		report.add(runOptionalCommand(ctx, root, "staticcheck ./...", "staticcheck", "./..."))
		report.add(runOptionalCommand(ctx, root, "govulncheck ./...", "govulncheck", "./..."))
		report.add(runCommand(ctx, root, "eval suite", "go", "test", "./internal/eval"))
		report.add(runCommand(ctx, root, "migration tests", "go", "test", "./internal/storage"))
		report.add(runCommand(ctx, root, "make release-build", "make", "release-build"))
		report.add(runCommand(ctx, root, "make checksums", "make", "checksums"))
	}
	if report.Failed() {
		return report, errors.New("release checks failed")
	}
	return report, nil
}

func (r *Report) add(checks ...Check) {
	r.Checks = append(r.Checks, checks...)
}

func checkRequiredFiles(root string) []Check {
	files := []string{
		"docs/p14_ga_release_requirements.md",
		"docs/release/quickstart.md",
		"docs/release/user_guide.md",
		"docs/release/admin_guide.md",
		"docs/release/security_threat_model.md",
		"docs/release/troubleshooting_runbook.md",
		"docs/release/performance_baseline.md",
		"docs/release/recovery_drill.md",
		"docs/release/rc_e2e_scenarios.md",
		"docs/release/soak_test.md",
		"docs/release/upgrade.md",
		"packaging/systemd/pachat.service",
		"packaging/launchd/com.personal-agent.pachat.plist",
		"scripts/install.sh",
		"scripts/upgrade.sh",
	}
	checks := make([]Check, 0, len(files))
	for _, file := range files {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			checks = append(checks, Check{Name: "required file", Status: "fail", Detail: file})
			continue
		}
		checks = append(checks, Check{Name: "required file", Status: "pass", Detail: file})
	}
	return checks
}

func runCommand(ctx context.Context, root string, name string, command string, args ...string) Check {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Check{Name: name, Status: "fail", Detail: fmt.Sprintf("%v: %s", err, string(output))}
	}
	return Check{Name: name, Status: "pass"}
}

func runOptionalCommand(ctx context.Context, root string, name string, command string, args ...string) Check {
	if _, err := exec.LookPath(command); err != nil {
		return Check{Name: name, Status: "skip", Detail: "tool not installed", Optional: true}
	}
	return runCommand(ctx, root, name, command, args...)
}
