package database

import (
	"testing"
)

func TestCanonicalizeJSON_Primitives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string // raw JSON input
		want    string // exact canonical bytes as a string
		wantErr bool
	}{

		{name: "NumberDecimal", in: "1.5", want: "\"3/2\""},
		{name: "NumberInteger", in: "2", want: "\"2/1\""},
		{name: "NumberExpPos", in: "1e3", want: "\"1000/1\""},
		{name: "NumberExpNeg", in: "1e-3", want: "\"1/1000\""},
		{name: "NumberZero", in: "0", want: "\"0/1\""},
		{name: "NumberNegZero", in: "-0.0", want: "\"0/1\""},

		// whitespace around a number token is allowed and ignored by the decoder
		{name: "NumberWithSpaces", in: "   1.5000   ", want: "\"3/2\""},

		// numeric strings
		{name: "StringNumericDecimal", in: "\"32.06\"", want: "\"1603/50\""},
		{name: "StringNumericInteger", in: "\"7\"", want: "\"7/1\""},
		{name: "StringNumericExp", in: "\"1e2\"", want: "\"100/1\""},

		{name: "StringNumericWithPlus", in: "\"+2.0\"", want: "\"2/1\""},

		// Very large integer string (beyond 64-bit), should work via big.Int
		{
			name: "StringNumericVeryLarge",
			in:   "\"123456789012345678901234567890\"",
			want: "\"123456789012345678901234567890/1\"",
		},

		// should stay a string
		{name: "StringLeadingZeros", in: "\"0012\"", want: "\"0012\""},

		// non-numeric strings remain unchanged
		{name: "StringRegular", in: "\"I like 2\"", want: "\"I like 2\""},
		{
			name: "StringWithEscapes",
			in:   "\"line\\nbreak\"",
			want: "\"line\\nbreak\"",
		},

		// booleans / null
		{name: "BoolTrue", in: "true", want: "true"},
		{name: "BoolFalse", in: "false", want: "false"},
		{name: "Null", in: "null", want: "null"},

		// unsupported types
		{name: "Array", in: "[]", wantErr: true},
		{name: "Object", in: "{}", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBytes, err := CanonicalizeJSON([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf(
						"expected error, got nil (output=%q)",
						string(gotBytes),
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := string(gotBytes)
			if got != tc.want {
				t.Fatalf(
					"canonical mismatch\nin:   %s\nwant: %s\ngot:  %s",
					tc.in,
					tc.want,
					got,
				)
			}
		})
	}
}

func TestCanonicalizeJSON_Reduction(t *testing.T) {
	t.Parallel()

	// fractions are reduced to simplest terms
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "StringTrailingZeros",
			in:   "\"12.3400\"",
			want: "\"617/50\"",
		}, // 1234/100 -> 617/50
		{name: "StringHalf", in: "\"0.5\"", want: "\"1/2\""},
		{name: "StringNegHalf", in: "\"-0.5\"", want: "\"-1/2\""},
		{
			name: "NumberThird",
			in:   "0.333333333",
			want: "\"333333333/1000000000\"",
		}, // exact decimal given
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CanonicalizeJSON([]byte(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf(
					"reduction mismatch\nin:   %s\nwant: %s\ngot:  %s",
					tc.in,
					tc.want,
					string(got),
				)
			}
		})
	}
}

func TestCanonicalizeJSON_NumberVsNumericStringEquivalence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		num string // JSON number
		str string // JSON string containing the same value
	}{
		{num: "1.5", str: "\"1.5\""},
		{num: "32.06", str: "\"32.06\""},
		{num: "1e-3", str: "\"1e-3\""},
		{num: "0", str: "\"0\""},
	}

	for _, tc := range cases {
		numOut, err := CanonicalizeJSON([]byte(tc.num))
		if err != nil {
			t.Fatalf("num=%s: %v", tc.num, err)
		}
		strOut, err := CanonicalizeJSON([]byte(tc.str))
		if err != nil {
			t.Fatalf("str=%s: %v", tc.str, err)
		}
		if string(numOut) != string(strOut) {
			t.Fatalf("canonical mismatch for %s vs %s:\nnumOut=%s\nstrOut=%s",
				tc.num, tc.str, string(numOut), string(strOut))
		}
	}
}
