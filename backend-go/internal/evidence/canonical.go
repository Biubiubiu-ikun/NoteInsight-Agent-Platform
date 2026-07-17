package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func NormalizeLineEndings(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func ContractHash(parserVersion string, canonicalText string) string {
	return hashParts(parserVersion, canonicalText)
}

func hashParts(parts ...string) string {
	hasher := sha256.New()
	for index, part := range parts {
		if index > 0 {
			_, _ = hasher.Write([]byte{0x1f})
		}
		_, _ = hasher.Write([]byte(part))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
