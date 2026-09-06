package version

import "testing"

func TestCurrentIncludesReleaseSchemas(t *testing.T) {
	info := Current()
	if info.Version == "" || info.ConfigSchema == "" || info.DatabaseSchema == "" || info.SkillManifest == "" || info.APIVersion == "" {
		t.Fatalf("incomplete version info: %+v", info)
	}
}
