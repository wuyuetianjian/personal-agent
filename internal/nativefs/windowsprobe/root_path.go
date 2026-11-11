package windowsprobe

import "strings"

// parseLocalRootPath validates only a deliberately narrow root-selection syntax.
// It does not resolve a drive, establish namespace trust or authorize tools.
func parseLocalRootPath(path string) (string, []string, error) {
	if len(path) < 3 || path[1] != ':' || path[2] != '\\' {
		return "", nil, errUnsafe
	}
	letter := path[0]
	if letter >= 'a' && letter <= 'z' {
		letter -= 'a' - 'A'
	}
	if letter < 'A' || letter > 'Z' {
		return "", nil, errUnsafe
	}
	drive := string([]byte{letter, ':'})
	if len(path) == 3 {
		return drive, nil, nil
	}
	rest := strings.TrimSuffix(path[3:], `\`)
	parts := strings.SplitN(rest, `\`, 33)
	if err := validateNames(parts, false); err != nil {
		return "", nil, err
	}
	return drive, parts, nil
}

// physicalVolumeRoot recognizes a direct physical-volume object name returned
// by a drive mapping query. The name is not proof of volume type, authorization,
// stable mapping or persistent identity; the opened object must still qualify.
func physicalVolumeRoot(target string) (string, error) {
	const prefix = `\Device\HarddiskVolume`
	if len(target) > 64 || !strings.HasPrefix(target, prefix) || len(target) == len(prefix) {
		return "", errUnsafe
	}
	for _, b := range []byte(target[len(prefix):]) {
		if b < '0' || b > '9' {
			return "", errUnsafe
		}
	}
	return target + `\`, nil
}
