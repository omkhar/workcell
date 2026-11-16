package metadatautil

import (
	"strings"
	"testing"
	"time"
)

func TestHeredocsAsHereStringsJoinsContinuedLinesInLinearWork(t *testing.T) {
	for name, shape := range map[string]func(int) string{
		"breaks between words": func(n int) string { return "echo " + strings.Repeat("a \\\n", n) + "b\n" },
		"breaks inside a word": func(n int) string { return "echo " + strings.Repeat("a\\\n", n) + "b\n" },
		"a continued opener":   func(n int) string { return "cat <<EOF " + strings.Repeat("a \\\n", n) + "b\nEOF\n" },
	} {
		t.Run(name, func(t *testing.T) {
			measure := func(n int) time.Duration {
				best := time.Duration(1 << 62)
				for range 3 {
					start := time.Now()
					heredocsAsHereStrings(shape(n))
					best = min(best, time.Since(start))
				}
				return best
			}
			n := 64
			for ; measure(n) < 20*time.Millisecond && n < 1<<16; n *= 2 {
			}
			// A busy machine can slow one size more than the other, so a
			// shape fails only when three attempts in a row exceed the ratio.
			for range 3 {
				small, large := measure(n), measure(4*n)
				if large <= 8*small {
					return
				}
				t.Logf("heredocsAsHereStrings() took %v at %d and %v at %d", small, n, large, 4*n)
			}
			t.Fatalf("heredocsAsHereStrings() took more than eight times as long for four times the text in three attempts")
		})
	}
}
