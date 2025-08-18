package tx

import (
	"testing"
)

func TestIsHex(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"1234567890abcdef", true},
		{"1234567890abcdef", true},
		{"0x1234567890abcdefg", false},
		{"deadbeef", true},
		{"50414c4d0a", true},
		{"palm_emissions_singleton", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := IsHex(test.input)
			if result != test.expected {
				t.Errorf(
					"IsHex(%q) = %v; want %v",
					test.input,
					result,
					test.expected,
				)
			}
		})
	}
}

func TestDecodeHexIfValid(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"50414c4d0a", "PALM\n"},
		{"deadbeef", "\xde\xad\xbe\xef"},
		{"palm_emissions_singleton", "palm_emissions_singleton"},
		{"invalid_hex_g", "invalid_hex_g"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := DecodeHexIfValid(test.input)
			if result != test.expected {
				t.Errorf(
					"DecodeHexIfValid(%q) = %q; want %q",
					test.input,
					result,
					test.expected,
				)
			}
		})
	}
}
