package event

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func DedupKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strings.TrimSpace(part)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
