// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package shellproto_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/shellproto"
	"github.com/omkhar/workcell/internal/testkit"
)

// The KEY=VALUE protocol answers the same corpus with rejection rather than
// with encoding: a value that carries a record separator is refused at the
// output boundary. Every other row must survive the bash reader's own
// "key=${line%%=*}; value=${line#*=}" split byte for byte.
func TestWriteFieldRoundTripsOrRejectsHostileFields(t *testing.T) {
	t.Parallel()
	encode := func(value string) (string, error) {
		var line strings.Builder
		if err := shellproto.WriteField(&line, "field", value); err != nil {
			return "", err
		}
		return line.String(), nil
	}
	decode := func(line string) (string, error) {
		trimmed, found := strings.CutSuffix(line, "\n")
		if !found {
			return "", errors.New("the record has no line terminator")
		}
		if strings.Contains(trimmed, "\n") {
			return "", errors.New("the record spans more than one line")
		}
		_, value, found := strings.Cut(trimmed, "=")
		if !found {
			return "", errors.New("the record has no key separator")
		}
		return value, nil
	}
	testkit.RequireRoundTripOrReject(t, encode, decode, testkit.HostileFields())
}
