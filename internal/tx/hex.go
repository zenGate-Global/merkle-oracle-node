package tx

import (
	"encoding/hex"
)

// IsHex checks if a string is a valid hex encoding
func IsHex(s string) bool {
	if len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// DecodeHexIfValid decodes a hex string to a regular string if it's valid hex,
// otherwise returns the original string
func DecodeHexIfValid(s string) string {
	if IsHex(s) {
		decoded, err := hex.DecodeString(s)
		if err != nil {
			return s // Return original if decode fails
		}
		return string(decoded)
	}
	return s
}
