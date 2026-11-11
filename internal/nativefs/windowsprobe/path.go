package windowsprobe

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var errUnsafe = errors.New("windowsprobe: unsupported or unsafe object")

func validateComponents(parts []string) error {
	if len(parts) == 0 || len(parts) > 32 {
		return errUnsafe
	}
	for _, s := range parts {
		if s == "" || !utf8.ValidString(s) || len(utf16.Encode([]rune(s))) > 255 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") || strings.ContainsAny(s, `/\:<>"|?*`) {
			return errUnsafe
		}
		for _, r := range s {
			if unicode.IsControl(r) {
				return errUnsafe
			}
		}
		upper := strings.ToUpper(s)
		if upper == "DATA" || upper == "NODE_MODULES" {
			return errUnsafe
		}
		base := strings.SplitN(upper, ".", 2)[0]
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			return errUnsafe
		}
		for _, prefix := range []string{"COM", "LPT"} {
			if strings.HasPrefix(base, prefix) && strings.Contains("123456789¹²³", strings.TrimPrefix(base, prefix)) && len([]rune(strings.TrimPrefix(base, prefix))) == 1 {
				return errUnsafe
			}
		}
	}
	return nil
}
