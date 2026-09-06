package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

func Load(path string) (Config, error) {
	report, err := Inspect(path)
	if err != nil {
		return Config{}, err
	}
	return report.Config, nil
}

type Report struct {
	Config   Config
	Warnings []string
}

func Inspect(path string) (Report, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Report{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}
	report := Report{Config: cfg}
	if cfg.ConfigVersion == 0 {
		report.Warnings = append(report.Warnings, "config_version is missing; treating file as legacy schema v1")
	}
	return report, nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == 0 || value.Value == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value.Value, err)
	}
	d.Duration = parsed
	return nil
}

func EnvValue(envName string) (string, bool) {
	if envName == "" {
		return "", false
	}
	value, ok := os.LookupEnv(envName)
	return value, ok && value != ""
}
