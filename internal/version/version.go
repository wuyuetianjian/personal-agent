package version

import (
	"runtime"
	"strings"
)

const (
	ApplicationVersion = "1.0.0"
	ConfigSchema       = "1"
	DatabaseSchema     = "0006_p14_pre_proactive.sql"
	SkillManifest      = "1"
	APIVersion         = "v1"
)

var (
	Version   = ApplicationVersion
	Commit    = "unknown"
	BuildDate = "unknown"
	GoVersion = runtime.Version()
)

type Info struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BuildDate      string `json:"build_date"`
	GoVersion      string `json:"go_version"`
	ConfigSchema   string `json:"config_schema"`
	DatabaseSchema string `json:"database_schema"`
	SkillManifest  string `json:"skill_manifest"`
	APIVersion     string `json:"api_version"`
}

func Current() Info {
	return Info{
		Version:        clean(Version, ApplicationVersion),
		Commit:         clean(Commit, "unknown"),
		BuildDate:      clean(BuildDate, "unknown"),
		GoVersion:      clean(GoVersion, runtime.Version()),
		ConfigSchema:   ConfigSchema,
		DatabaseSchema: DatabaseSchema,
		SkillManifest:  SkillManifest,
		APIVersion:     APIVersion,
	}
}

func clean(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
