// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package cliexit

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeErrorErrorReturnsMessage(t *testing.T) {
	err := &ExitCodeError{Code: 2, Message: "boom"}
	if got := err.Error(); got != "boom" {
		t.Errorf("Error() = %q, want %q", got, "boom")
	}
}

func TestIsExitCodeErrorMatchesDirect(t *testing.T) {
	want := &ExitCodeError{Code: 7, Message: "x"}
	got, ok := IsExitCodeError(want)
	if !ok {
		t.Fatalf("IsExitCodeError returned ok=false for direct value")
	}
	if got != want {
		t.Errorf("IsExitCodeError pointer = %v, want %v", got, want)
	}
}

func TestIsExitCodeErrorUnwrapsWrapped(t *testing.T) {
	inner := &ExitCodeError{Code: 3, Message: "inner"}
	wrapped := fmt.Errorf("outer: %w", inner)
	got, ok := IsExitCodeError(wrapped)
	if !ok {
		t.Fatalf("IsExitCodeError returned ok=false for wrapped value")
	}
	if got.Code != 3 {
		t.Errorf("got.Code = %d, want 3", got.Code)
	}
}

func TestIsExitCodeErrorRejectsOtherErrors(t *testing.T) {
	got, ok := IsExitCodeError(errors.New("plain"))
	if ok || got != nil {
		t.Fatalf("IsExitCodeError matched a plain error: got=%v ok=%v", got, ok)
	}
}
