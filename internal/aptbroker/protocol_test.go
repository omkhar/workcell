// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRequestFrameRoundTripPreservesPolicyFields(t *testing.T) {
	request := Request{
		Args: []string{"apt-get", "install", "curl"},
		Env: map[string]string{
			"DEBIAN_FRONTEND":          "noninteractive",
			"APT_LISTCHANGES_FRONTEND": "text",
		},
	}
	encoded, err := requestFrame(request)
	if err != nil {
		t.Fatal(err)
	}
	assertFrameHeader(t, encoded, requestMagic)
	decoded, err := decodeRequest(encoded[frameHeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, request) {
		t.Fatalf("decoded request = %#v, want %#v", decoded, request)
	}
}

func TestRequestFrameMatchesProtocolBytes(t *testing.T) {
	encoded, err := requestFrame(Request{Args: []string{"apt-get"}, Env: map[string]string{"DEBIAN_FRONTEND": "noninteractive"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "5743425201000000300001000000076170742d676574010f44454249414e5f46524f4e54454e440000000e6e6f6e696e746572616374697665"
	if got := hex.EncodeToString(encoded); got != want {
		t.Fatalf("request frame = %s, want %s", got, want)
	}
}

func TestRequestPolicyRejectsInvalidBoundsAndEnvironment(t *testing.T) {
	testCases := []struct {
		name    string
		request Request
	}{
		{name: "no arguments", request: Request{}},
		{name: "too many arguments", request: Request{Args: make([]string, MaxArguments+1)}},
		{name: "too many environment values", request: Request{Args: []string{"apt-get"}, Env: map[string]string{"DEBIAN_FRONTEND": "noninteractive", "DEBCONF_NONINTERACTIVE_SEEN": "true", "APT_LISTCHANGES_FRONTEND": "none", "PATH": "fixed"}}},
		{name: "unsupported environment name", request: Request{Args: []string{"apt-get"}, Env: map[string]string{"PATH": "/tmp"}}},
		{name: "interactive frontend", request: Request{Args: []string{"apt-get"}, Env: map[string]string{"APT_LISTCHANGES_FRONTEND": "browser"}}},
		{name: "invalid debconf value", request: Request{Args: []string{"apt-get"}, Env: map[string]string{"DEBCONF_NONINTERACTIVE_SEEN": "false"}}},
		{name: "invalid Debian frontend", request: Request{Args: []string{"apt-get"}, Env: map[string]string{"DEBIAN_FRONTEND": "dialog"}}},
		{name: "oversized frame", request: Request{Args: []string{strings.Repeat("x", MaxRequestBytes)}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := requestFrame(testCase.request); err == nil {
				t.Fatal("requestFrame() accepted invalid request")
			}
		})
	}
}

func TestRequestPolicyAcceptsEveryApprovedEnvironmentValue(t *testing.T) {
	for name, values := range allowedEnvironmentValues {
		for value := range values {
			request := Request{Args: []string{"apt-get"}, Env: map[string]string{name: value}}
			if _, err := requestFrame(request); err != nil {
				t.Errorf("requestFrame() rejected %s=%s: %v", name, value, err)
			}
		}
	}
}

func TestRequestPolicyAcceptsExactBounds(t *testing.T) {
	arguments := make([]string, MaxArguments)
	arguments[0] = "apt-get"
	environment := map[string]string{
		"APT_LISTCHANGES_FRONTEND":    "none",
		"DEBCONF_NONINTERACTIVE_SEEN": "true",
		"DEBIAN_FRONTEND":             "noninteractive",
	}
	if _, err := requestFrame(Request{Args: arguments, Env: environment}); err != nil {
		t.Fatalf("requestFrame() rejected argument and environment bounds: %v", err)
	}
	exactBodyArgument := strings.Repeat("x", MaxRequestBytes-7)
	frame, err := requestFrame(Request{Args: []string{exactBodyArgument}})
	if err != nil {
		t.Fatalf("requestFrame() rejected exact byte limit: %v", err)
	}
	if got := len(frame) - frameHeaderSize; got != MaxRequestBytes {
		t.Fatalf("request body length = %d, want %d", got, MaxRequestBytes)
	}
}

func TestDecodeRequestRejectsMalformedFields(t *testing.T) {
	valid, err := encodeRequest(Request{Args: []string{"apt-get"}, Env: map[string]string{"DEBIAN_FRONTEND": "noninteractive"}})
	if err != nil {
		t.Fatal(err)
	}
	testCases := map[string][]byte{
		"empty":                  nil,
		"truncated":              valid[:len(valid)-1],
		"trailing data":          append(append([]byte(nil), valid...), 0),
		"invalid argument count": append([]byte{0, 0}, valid[2:]...),
		"oversized":              make([]byte, MaxRequestBytes+1),
	}
	for name, body := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRequest(body); err == nil {
				t.Fatal("decodeRequest() accepted malformed body")
			}
		})
	}
}

func TestDecodeRequestRejectsDuplicateEnvironmentName(t *testing.T) {
	body := requestBodyWithDuplicateEnvironment(t)
	if _, err := decodeRequest(body); err == nil || !strings.Contains(err.Error(), "duplicate environment name") {
		t.Fatalf("decodeRequest() error = %v, want duplicate environment name", err)
	}
}

func TestResponseFrameRoundTripPreservesStatusAndStreams(t *testing.T) {
	want := Response{Status: 124, Stdout: []byte("out"), Stderr: []byte("err")}
	encoded, err := responseFrame(want)
	if err != nil {
		t.Fatal(err)
	}
	assertFrameHeader(t, encoded, responseMagic)
	got, err := readResponse(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readResponse() = %#v, want %#v", got, want)
	}
}

func TestResponseFrameMatchesProtocolBytes(t *testing.T) {
	encoded, err := responseFrame(Response{Status: 37, Stdout: []byte("out"), Stderr: []byte("err")})
	if err != nil {
		t.Fatal(err)
	}
	want := "574352530100000010002500000003000000036f7574657272"
	if got := hex.EncodeToString(encoded); got != want {
		t.Fatalf("response frame = %s, want %s", got, want)
	}
}

func TestResponsePolicyRejectsInvalidBounds(t *testing.T) {
	for _, response := range []Response{
		{Status: -1},
		{Status: 256},
		{Stdout: make([]byte, MaxOutputBytes+1)},
		{Stderr: make([]byte, MaxOutputBytes+1)},
	} {
		if _, err := responseFrame(response); err == nil {
			t.Fatalf("responseFrame() accepted %#v", response)
		}
	}
}

func TestResponsePolicyAcceptsEachExactStreamLimit(t *testing.T) {
	response := Response{
		Status: 255,
		Stdout: make([]byte, MaxOutputBytes),
		Stderr: make([]byte, MaxOutputBytes),
	}
	encoded, err := responseFrame(response)
	if err != nil {
		t.Fatalf("responseFrame() rejected exact bounds: %v", err)
	}
	decoded, err := readResponse(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, response) {
		t.Fatal("response round trip changed exact-bound streams")
	}
}

func TestReadResponseRejectsStatusOutsideProcessExitDomain(t *testing.T) {
	encoded, err := responseFrame(Response{})
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint16(encoded[frameHeaderSize:], 256)
	if _, err := readResponse(bytes.NewReader(encoded)); err == nil {
		t.Fatal("readResponse() accepted status 256")
	}
}

func TestReadResponseRejectsMalformedFrame(t *testing.T) {
	valid, err := responseFrame(Response{Status: 37})
	if err != nil {
		t.Fatal(err)
	}
	wrongMagic := append([]byte(nil), valid...)
	copy(wrongMagic, "FAIL")
	wrongVersion := append([]byte(nil), valid...)
	wrongVersion[4]++
	oversized := append([]byte(nil), valid[:frameHeaderSize]...)
	binary.BigEndian.PutUint32(oversized[5:], uint32(responseBaseSize+2*MaxOutputBytes+1))
	for name, frame := range map[string][]byte{
		"wrong magic":   wrongMagic,
		"wrong version": wrongVersion,
		"oversized":     oversized,
		"truncated":     valid[:len(valid)-1],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readResponse(bytes.NewReader(frame)); err == nil {
				t.Fatal("readResponse() accepted malformed frame")
			}
		})
	}
}

func assertFrameHeader(t *testing.T, encoded []byte, magic string) {
	t.Helper()
	if got := string(encoded[:4]); got != magic {
		t.Fatalf("frame magic = %q, want %q", got, magic)
	}
	if encoded[4] != protocolVersion {
		t.Fatalf("protocol version = %d, want %d", encoded[4], protocolVersion)
	}
	if got, want := binary.BigEndian.Uint32(encoded[5:]), uint32(len(encoded)-frameHeaderSize); got != want {
		t.Fatalf("body length = %d, want %d", got, want)
	}
}

func requestBodyWithDuplicateEnvironment(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, uint16(1))
	writeString(&body, "apt-get")
	_ = body.WriteByte(2)
	for range 2 {
		_ = body.WriteByte(byte(len("DEBIAN_FRONTEND")))
		_, _ = body.WriteString("DEBIAN_FRONTEND")
		writeString(&body, "noninteractive")
	}
	return body.Bytes()
}

// A pathname past sun_path must be refused with a message naming the limit and
// the offending length, not with the kernel's bare "invalid argument", and both
// endpoints must refuse it before they touch the filesystem.  A long TMPDIR is
// how an over-long path reaches this package, which is also why the suite's own
// binds go through shortSocketDir.
func TestSocketPathOverSunPathLimitIsRejectedWithTheLimit(t *testing.T) {
	over := "/tmp/" + strings.Repeat("p", maxSocketPathLength) + "/socket"
	atLimit := "/tmp/" + strings.Repeat("p", maxSocketPathLength-len("/tmp//socket")) + "/socket"
	if len(atLimit) != maxSocketPathLength {
		t.Fatalf("boundary fixture is %d bytes, want %d", len(atLimit), maxSocketPathLength)
	}
	want := fmt.Sprintf("apt broker socket path is %d bytes, over the %d-byte AF_UNIX limit: %s",
		len(over), maxSocketPathLength, over)

	if _, err := listenSocket(over, false); err == nil || err.Error() != want {
		t.Fatalf("listenSocket error = %v, want %s", err, want)
	}
	if _, err := dialBroker(context.Background(), over); err == nil || err.Error() != want {
		t.Fatalf("dialBroker error = %v, want %s", err, want)
	}
	if err := checkSocketPathLength(atLimit); err != nil {
		t.Fatalf("path at the limit rejected: %v", err)
	}
}
