package release

import (
	"os"
	"path/filepath"
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
