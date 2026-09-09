package release

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRequiredFileChecks(t *testing.T) {
	dir := t.TempDir()
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
	for _, file := range files {
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range checkRequiredFiles(dir) {
		if check.Status != "pass" {
			t.Fatalf("checkRequiredFiles() returned failing check: %+v", check)
		}
	}
}

func TestFunctionalGACheckSpecsIncludeMandatoryGates(t *testing.T) {
	specs := functionalGACheckSpecs()
	gotNames := make([]string, 0, len(specs))
	byName := make(map[string]commandCheckSpec, len(specs))
	for _, spec := range specs {
		gotNames = append(gotNames, spec.Name)
		byName[spec.Name] = spec
	}
	wantNames := []string{
		"functional rc e2e",
		"runtime leader planning",
		"external executor registry",
		"workflow restart recovery",
		"privacy escalation",
		"early-stop cancellation",
		"side-effect idempotency",
		"notification edge semantics",
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("functionalGACheckSpecs() names = %v, want %v", gotNames, wantNames)
	}
	for _, name := range wantNames {
		spec := byName[name]
		if spec.Command != "go" {
			t.Fatalf("%s command = %q, want go", name, spec.Command)
		}
		if len(spec.Args) == 0 || spec.Args[0] != "test" {
			t.Fatalf("%s args = %v, want go test args", name, spec.Args)
		}
		if !containsArg(spec.Args, "-count=1") {
			t.Fatalf("%s args = %v, want -count=1", name, spec.Args)
		}
	}
	assertArgsContain(t, byName["functional rc e2e"], "./internal/e2e", "FunctionalRC")
	assertArgsContain(t, byName["external executor registry"], "./internal/runtime", "TestWorkflowEngineExecutesRegistered")
	assertArgsContain(t, byName["notification edge semantics"], "./internal/notification", "TestNotificationPolicyDedupSeverityAndQuietHours")
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func assertArgsContain(t *testing.T, spec commandCheckSpec, wants ...string) {
	t.Helper()
	joined := strings.Join(spec.Args, " ")
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s args = %v, want to contain %q", spec.Name, spec.Args, want)
		}
	}
}
