// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package policybundle

import (
	"strconv"
	"testing"
	"unicode/utf8"
)

func FuzzParseHexByte(f *testing.F) {
	for _, seed := range []string{"00", "09", "7f", "80", "ff", "", "0", "fff", "gg"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		value, err := parseHexByte(text)
		want, wantErr := strconv.ParseUint(text, 16, 8)
		if wantErr != nil {
			if err == nil {
				t.Fatalf("parseHexByte(%q) unexpectedly succeeded with %d", text, value)
			}
			return
		}
		if err != nil {
			t.Fatalf("parseHexByte(%q) error = %v, want success", text, err)
		}
		if value != byte(want) {
			t.Fatalf("parseHexByte(%q) = %d, want %d", text, value, byte(want))
		}
	})
}

func FuzzParseHexRune(f *testing.F) {
	for _, seed := range []struct {
		text    string
		bitSize int
	}{
		{text: "41", bitSize: 8},
		{text: "03bb", bitSize: 16},
		{text: "1f600", bitSize: 32},
		{text: "", bitSize: 8},
		{text: "110000", bitSize: 32},
		{text: "zz", bitSize: 16},
	} {
		f.Add(seed.text, seed.bitSize)
	}

	f.Fuzz(func(t *testing.T, text string, bitSize int) {
		switch bitSize {
		case 8, 16, 32:
		default:
			return
		}

		value, err := parseHexRune(text, bitSize)
		want, wantErr := strconv.ParseUint(text, 16, bitSize)
		switch {
		case wantErr != nil:
			if err == nil {
				t.Fatalf("parseHexRune(%q, %d) unexpectedly succeeded with %q", text, bitSize, value)
			}
		case want > utf8.MaxRune:
			if err == nil {
				t.Fatalf("parseHexRune(%q, %d) unexpectedly accepted invalid rune %U", text, bitSize, rune(want))
			}
		default:
			if err != nil {
				t.Fatalf("parseHexRune(%q, %d) error = %v, want success", text, bitSize, err)
			}
			if value != rune(want) {
				t.Fatalf("parseHexRune(%q, %d) = %U, want %U", text, bitSize, value, rune(want))
			}
		}
	})
}
