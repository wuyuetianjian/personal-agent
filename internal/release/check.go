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

type commandCheckSpec struct {
	Name    string
	Command string
	Args    []string
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
	report.add(functionalGAChecks(ctx, root)...)
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

func functionalGAChecks(ctx context.Context, root string) []Check {
	specs := functionalGACheckSpecs()
	checks := make([]Check, 0, len(specs))
	for _, spec := range specs {
		checks = append(checks, runCommand(ctx, root, spec.Name, spec.Command, spec.Args...))
	}
	return checks
}

func functionalGACheckSpecs() []commandCheckSpec {
	return []commandCheckSpec{
		{
			Name:    "functional rc e2e",
			Command: "go",
			Args:    []string{"test", "./internal/e2e", "-run", "FunctionalRC", "-count=1"},
		},
		{
			Name:    "runtime leader planning",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestBuildWiresConfiguredProviderIntoPlanner|TestBoundedModelPlannerAcceptsBoundedDAG", "-count=1"},
		},
		{
			Name:    "external executor registry",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestWorkflowEngineExecutesRegisteredBrowserReadExecutor|TestWorkflowEngineExecutesRegisteredCodingExecutor|TestWorkflowEngineExecutesRegisteredMCPExecutor|TestWorkflowEngineExecutesAllowlistedToolExecutor", "-count=1"},
		},
		{
			Name:    "workflow restart recovery",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestWorkflowWorkerRecoversRunnableWorkflowOnStartup", "-count=1"},
		},
		{
			Name:    "privacy escalation",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestBuildWiresConfiguredPublicEscalatorWithPrivacyGateway|TestPublicEscalatorPrivacyGatewayBlocksSecretsBeforeProviderCall", "-count=1"},
		},
		{
			Name:    "early-stop cancellation",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestWorkflowVerificationEarlyStopCancelsRemainingWork", "-count=1"},
		},
		{
			Name:    "side-effect idempotency",
			Command: "go",
			Args:    []string{"test", "./internal/runtime", "-run", "TestSideEffectIdempotencySkipsCompletedDuplicate|TestSideEffectIdempotencyBlocksRecoveryDuplicate", "-count=1"},
		},
		{
			Name:    "notification edge semantics",
			Command: "go",
			Args:    []string{"test", "./internal/notification", "-run", "TestNotificationPolicyDedupSeverityAndQuietHours", "-count=1"},
		},
	}
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
