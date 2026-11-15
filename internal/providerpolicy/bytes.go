// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerpolicy

import "strings"

// beforeEqual returns s up to its first "=", or all of s.
func beforeEqual(s string) string {
	name, _, _ := strings.Cut(s, "=")
	return name
}

// asciiLower folds A to Z only, as bash ${s,,} does in the C locale.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// cSpace is the C-locale [[:space:]] class.
const cSpace = " \t\n\v\f\r"

// isUnsafeByte matches the C-locale class [^[:print:]\t]: a control byte
// other than tab, DEL, or a non-ASCII byte.
func isUnsafeByte(c byte) bool {
	return c != '\t' && (c < 0x20 || c > 0x7e)
}

func hasUnsafeByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if isUnsafeByte(s[i]) {
			return true
		}
	}
	return false
}
