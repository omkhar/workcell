// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerid

import "testing"

func TestCopilotRemainsPlannedUntilCertified(t *testing.T) {
	if Copilot != "copilot" {
		t.Fatalf("Copilot = %q, want copilot", Copilot)
	}
	if IsValid(Copilot) {
		t.Fatal("Copilot must stay out of the supported-provider set until runtime support and certification land")
	}
}
