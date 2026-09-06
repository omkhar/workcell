// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
)

const (
	DefaultSocketPath = "/run/workcell/apt-broker/socket"
	MaxRequestBytes   = 512 * 1024
	MaxArguments      = 64
	MaxEnvironment    = 3
	MaxOutputBytes    = 1 * 1024 * 1024

	protocolVersion  = 1
	requestMagic     = "WCBR"
	responseMagic    = "WCRS"
	frameHeaderSize  = 9
	responseBaseSize = 10
)

var allowedEnvironmentValues = map[string]map[string]struct{}{
	"APT_LISTCHANGES_FRONTEND": {
		"none": {},
		"text": {},
	},
	"DEBCONF_NONINTERACTIVE_SEEN": {
		"true": {},
	},
	"DEBIAN_FRONTEND": {
		"noninteractive": {},
	},
}

type Request struct {
	Args []string
	Env  map[string]string
}

type Response struct {
	Status int
	Stdout []byte
	Stderr []byte
}

func requestFrame(request Request) ([]byte, error) {
	body, err := encodeRequest(request)
	if err != nil {
		return nil, err
	}
	return frame(requestMagic, body), nil
}

func responseFrame(response Response) ([]byte, error) {
	body, err := encodeResponse(response)
	if err != nil {
		return nil, err
	}
	return frame(responseMagic, body), nil
}

func frame(magic string, body []byte) []byte {
	result := make([]byte, frameHeaderSize+len(body))
	copy(result, magic)
	result[4] = protocolVersion
	binary.BigEndian.PutUint32(result[5:], uint32(len(body)))
	copy(result[frameHeaderSize:], body)
	return result
}

func readFrame(reader io.Reader, magic string, maxBody uint32) ([]byte, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if string(header[:4]) != magic || header[4] != protocolVersion {
		return nil, fmt.Errorf("invalid apt broker frame")
	}
	bodyLen := binary.BigEndian.Uint32(header[5:])
	if bodyLen > maxBody {
		return nil, fmt.Errorf("apt broker frame exceeds limit")
	}
	body := make([]byte, int(bodyLen))
	_, err := io.ReadFull(reader, body)
	return body, err
}

func encodeRequest(request Request) ([]byte, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, uint16(len(request.Args)))
	for _, arg := range request.Args {
		writeString(&body, arg)
	}
	_ = body.WriteByte(byte(len(request.Env)))
	for _, name := range sortedEnvironment(request.Env) {
		_ = body.WriteByte(byte(len(name)))
		_, _ = body.WriteString(name)
		writeString(&body, request.Env[name])
	}
	if body.Len() > MaxRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", MaxRequestBytes)
	}
	return body.Bytes(), nil
}

func decodeRequest(body []byte) (Request, error) {
	if len(body) > MaxRequestBytes {
		return Request{}, fmt.Errorf("request exceeds %d bytes", MaxRequestBytes)
	}
	parser := newParser(body)
	args, err := parser.arguments()
	if err != nil {
		return Request{}, err
	}
	environment, err := parser.environment()
	if err != nil {
		return Request{}, err
	}
	if parser.remaining() != 0 {
		return Request{}, fmt.Errorf("trailing request data")
	}
	return Request{Args: args, Env: environment}, nil
}

func encodeResponse(response Response) ([]byte, error) {
	if err := validateResponse(response); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, uint16(response.Status))
	_ = binary.Write(&body, binary.BigEndian, uint32(len(response.Stdout)))
	_ = binary.Write(&body, binary.BigEndian, uint32(len(response.Stderr)))
	_, _ = body.Write(response.Stdout)
	_, _ = body.Write(response.Stderr)
	return body.Bytes(), nil
}

func decodeResponse(body []byte) (Response, error) {
	parser := newParser(body)
	status, stdoutLength, stderrLength, err := parser.responseHeader()
	if err != nil {
		return Response{}, err
	}
	stdout, err := parser.output(stdoutLength, "stdout")
	if err != nil {
		return Response{}, err
	}
	stderr, err := parser.output(stderrLength, "stderr")
	if err != nil || parser.remaining() != 0 {
		return Response{}, fmt.Errorf("invalid response stderr")
	}
	return Response{Status: int(status), Stdout: stdout, Stderr: stderr}, nil
}

func validateRequest(request Request) error {
	if err := validateArgumentCount(len(request.Args)); err != nil {
		return err
	}
	if err := validateArgumentLengths(request.Args); err != nil {
		return err
	}
	return validateEnvironment(request.Env)
}

func validateEnvironment(environment map[string]string) error {
	if len(environment) > MaxEnvironment {
		return fmt.Errorf("invalid request environment bounds")
	}
	for name, value := range environment {
		if !isAllowedEnvironmentValue(name, value) {
			return fmt.Errorf("unsupported environment value for %s", name)
		}
	}
	return nil
}

func validateArgumentLengths(args []string) error {
	for _, arg := range args {
		if len(arg) > MaxRequestBytes {
			return fmt.Errorf("request exceeds %d bytes", MaxRequestBytes)
		}
	}
	return nil
}

func validateArgumentCount(count int) error {
	if count == 0 || count > MaxArguments {
		return fmt.Errorf("invalid request argument bounds")
	}
	return nil
}

func validateResponse(response Response) error {
	if err := validateStatus(response.Status); err != nil {
		return err
	}
	if len(response.Stdout) > MaxOutputBytes || len(response.Stderr) > MaxOutputBytes {
		return fmt.Errorf("response exceeds output limit")
	}
	return nil
}

func validateStatus(status int) error {
	if status < 0 || status > 255 {
		return fmt.Errorf("invalid response status")
	}
	return nil
}

func isAllowedEnvironment(name string) bool {
	_, ok := allowedEnvironmentValues[name]
	return ok
}

func isAllowedEnvironmentValue(name, value string) bool {
	values, ok := allowedEnvironmentValues[name]
	if !ok {
		return false
	}
	_, ok = values[value]
	return ok
}

func writeString(body *bytes.Buffer, value string) {
	_ = binary.Write(body, binary.BigEndian, uint32(len(value)))
	_, _ = body.WriteString(value)
}

func sortedEnvironment(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

type parser struct {
	reader *bytes.Reader
}

func newParser(body []byte) *parser { return &parser{reader: bytes.NewReader(body)} }

func (p *parser) arguments() ([]string, error) {
	count, err := p.u16()
	if err != nil {
		return nil, fmt.Errorf("invalid argument count")
	}
	if err := validateArgumentCount(int(count)); err != nil {
		return nil, fmt.Errorf("invalid argument count")
	}
	args := make([]string, 0, count)
	for range count {
		arg, err := p.string()
		if err != nil {
			return nil, fmt.Errorf("invalid argument")
		}
		args = append(args, arg)
	}
	return args, nil
}

func (p *parser) environment() (map[string]string, error) {
	count, err := p.environmentCount()
	if err != nil {
		return nil, err
	}
	environment := make(map[string]string, count)
	for range count {
		name, value, err := p.environmentEntry()
		if err != nil {
			return nil, err
		}
		if err := addEnvironmentEntry(environment, name, value); err != nil {
			return nil, err
		}
	}
	return environment, nil
}

func (p *parser) environmentCount() (byte, error) {
	count, err := p.byte()
	if err != nil || int(count) > MaxEnvironment {
		return 0, fmt.Errorf("invalid environment count")
	}
	return count, nil
}

func addEnvironmentEntry(environment map[string]string, name, value string) error {
	if _, exists := environment[name]; exists {
		return fmt.Errorf("duplicate environment name")
	}
	environment[name] = value
	return nil
}

func (p *parser) environmentEntry() (string, string, error) {
	name, err := p.environmentName()
	if err != nil {
		return "", "", err
	}
	value, err := p.environmentValue(name)
	if err != nil {
		return "", "", err
	}
	return name, value, nil
}

func (p *parser) environmentName() (string, error) {
	nameLength, err := p.byte()
	if err != nil || nameLength == 0 {
		return "", fmt.Errorf("invalid environment name")
	}
	name, err := p.raw(int(nameLength))
	if err != nil || !isAllowedEnvironment(name) {
		return "", fmt.Errorf("unsupported environment name")
	}
	return name, nil
}

func (p *parser) environmentValue(name string) (string, error) {
	value, err := p.string()
	if err != nil || !isAllowedEnvironmentValue(name, value) {
		return "", fmt.Errorf("invalid environment value")
	}
	return value, nil
}

func (p *parser) output(length uint32, name string) ([]byte, error) {
	value, err := p.raw(int(length))
	if err != nil {
		return nil, fmt.Errorf("invalid response %s", name)
	}
	return []byte(value), nil
}

func (p *parser) responseHeader() (uint16, uint32, uint32, error) {
	status, err := p.u16()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid response status")
	}
	if err := validateStatus(int(status)); err != nil {
		return 0, 0, 0, err
	}
	stdoutLength, err := p.outputLength("stdout")
	if err != nil {
		return 0, 0, 0, err
	}
	stderrLength, err := p.outputLength("stderr")
	return status, stdoutLength, stderrLength, err
}

func (p *parser) outputLength(name string) (uint32, error) {
	length, err := p.u32()
	if err != nil || length > MaxOutputBytes {
		return 0, fmt.Errorf("invalid response %s", name)
	}
	return length, nil
}

func (p *parser) byte() (byte, error) {
	value, err := p.reader.ReadByte()
	return value, err
}

func (p *parser) u16() (uint16, error) {
	var value uint16
	err := binary.Read(p.reader, binary.BigEndian, &value)
	return value, err
}

func (p *parser) u32() (uint32, error) {
	var value uint32
	err := binary.Read(p.reader, binary.BigEndian, &value)
	return value, err
}

func (p *parser) string() (string, error) {
	length, err := p.u32()
	if err != nil {
		return "", err
	}
	return p.raw(int(length))
}

func (p *parser) raw(length int) (string, error) {
	if length < 0 || length > p.reader.Len() {
		return "", io.ErrUnexpectedEOF
	}
	value := make([]byte, length)
	_, err := io.ReadFull(p.reader, value)
	return string(value), err
}

func (p *parser) remaining() int { return p.reader.Len() }
