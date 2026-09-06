package codingagent

import (
	"os"
	"strings"
)

var allowedEnvPrefixes = []string{
	"HOME=",
	"LANG=",
	"LC_",
	"PATH=",
	"SHELL=",
	"TMPDIR=",
	"USER=",
}

var deniedEnvFragments = []string{
	"API_KEY",
	"AUTH",
	"COOKIE",
	"CREDENTIAL",
	"PASSWORD",
	"SECRET",
	"TOKEN",
}

func SanitizedEnvironment(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		if allowedEnv(entry) {
			env = append(env, entry)
		}
	}
	for _, entry := range extra {
		if !secretLike(entry) {
			env = append(env, entry)
		}
	}
	return env
}

func allowedEnv(entry string) bool {
	if secretLike(entry) {
		return false
	}
	for _, prefix := range allowedEnvPrefixes {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func secretLike(entry string) bool {
	name, _, _ := strings.Cut(entry, "=")
	name = strings.ToUpper(name)
	for _, fragment := range deniedEnvFragments {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}
