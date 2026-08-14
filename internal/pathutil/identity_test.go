// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package pathutil

import (
	"errors"
	"strings"
	"testing"
)

func TestCollisionKeyUnicodeAliases(t *testing.T) {
	for _, pair := range [][2]string{{"café", "cafe\u0301"}, {"straße", "STRASSE"}, {"Σ", "ς"}, {"ﬀ", "ff"}, {"µ", "Μ"}, {"ś", "ſ\u0301"}} {
		left, err := CollisionKey(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		right, err := CollisionKey(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if left != right {
			t.Fatalf("keys differ: %q and %q", pair[0], pair[1])
		}
	}
}

func TestPathIdentityRejectsEmptyAndInvalidUTF8(t *testing.T) {
	invalid := "secret-prefix-" + string([]byte{0xff})
	if _, err := CollisionKey(invalid); !errors.Is(err, ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("CollisionKey invalid error = %v", err)
	}
	if _, err := WithinOrEqual("root", invalid, false); !errors.Is(err, ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("WithinOrEqual invalid candidate error = %v", err)
	}
	if _, err := CollisionKey(""); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty error = %v, want ErrEmptyPath", err)
	}
}

func TestPathIdentityRejectsUnsafeControlRunes(t *testing.T) {
	for _, testCase := range []struct {
		name string
		path string
		want error
	}{
		{name: "newline", path: "secret-prefix-\n", want: ErrUnsafePathControl},
		{name: "escape", path: "secret-prefix-\x1b", want: ErrUnsafePathControl},
		{name: "line separator", path: "secret-prefix-\u2028", want: ErrUnsafePathControl},
		{name: "paragraph separator", path: "secret-prefix-\u2029", want: ErrUnsafePathControl},
		{name: "valid", path: "secret-prefix-safe"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := CollisionKey(testCase.path)
			if testCase.want == nil {
				if err != nil {
					t.Fatalf("CollisionKey() error = %v", err)
				}
				return
			}
			if !errors.Is(err, testCase.want) || strings.Contains(err.Error(), "secret-prefix") {
				t.Fatalf("CollisionKey() error = %v", err)
			}
		})
	}
}

func TestWithinOrEqualUnicodeAndBoundaries(t *testing.T) {
	inside, err := WithinOrEqual("/tmp/café", "/tmp/cafe\u0301/file", false)
	if err != nil || !inside {
		t.Fatalf("NFC containment = %v, %v", inside, err)
	}
	inside, err = WithinOrEqual("/tmp/straße", "/tmp/STRASSE/file", true)
	if err != nil || !inside {
		t.Fatalf("fold containment = %v, %v", inside, err)
	}
	inside, err = WithinOrEqual("/tmp/ś", "/tmp/ſ\u0301/file", true)
	if err != nil || !inside {
		t.Fatalf("post-fold NFC containment = %v, %v", inside, err)
	}
	inside, err = WithinOrEqual("/tmp/root", "/tmp/root-other/file", true)
	if err != nil || inside {
		t.Fatalf("sibling containment = %v, %v", inside, err)
	}
}

func TestCollisionKeyConcurrent(t *testing.T) {
	values := []string{"café", "cafe\u0301", "straße", "STRASSE", "Σ", "ς"}
	done := make(chan struct{})
	for range 32 {
		go func() {
			for _, value := range values {
				if _, err := CollisionKey(value); err != nil {
					t.Error(err)
				}
			}
			done <- struct{}{}
		}()
	}
	for range 32 {
		<-done
	}
}
