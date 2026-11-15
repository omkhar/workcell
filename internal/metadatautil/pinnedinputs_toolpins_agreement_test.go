package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolPinLoaderAcceptsExactlyTheGoFormats runs the bash loader and the Go
// parse on the same policy, one candidate value per key at a time, and requires
// them to accept and reject the same values. The candidates are the near
// misses a looser pattern would let through.
func TestToolPinLoaderAcceptsExactlyTheGoFormats(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	loader := filepath.Join(root, filepath.FromSlash(toolPinsLoaderRel))
	base, err := loadToolPins(filepath.Join(root, "policy", "tool-pins.toml"))
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	candidates := []string{
		"v1.2.3", "1.2.3", "v10.20.30", "10.20.30",
		"", "v1", "1", "v1.2", "1.2", "v1.2.3.4", "1.2.3.4", "V1.2.3", "vv1.2.3",
		"v1.2.3-rc.1", "1.2.3-rc.1", "v1.2.3+build", " v1.2.3", "v1.2.3 ", "v1..3", "v1.2.",
		"v١.2.3", "١.2.3", "v1.2.3\x00",
		"v0.0.0", "0.0.0", "v1.0.10", "1.0.10", "v01.2.3", "01.2.3", "v1.02.3", "1.2.03", "v00.1.2", "00.1.2", "v1.2.00",
		digest, strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("g", 64),
		"moby/buildkit:buildx-stable-1@sha256:" + digest,
		"moby/buildkit:v0.20.0@sha256:" + digest,
		"tonistiigi/binfmt:qemu-v10.2.3-68@sha256:" + digest,
		"ghcr.io/evil/moby/buildkit:buildx-stable-1@sha256:" + digest,
		"docker.io/moby/buildkit:buildx-stable-1@sha256:" + digest,
		"docker.io/tonistiigi/binfmt:qemu@sha256:" + digest,
		"moby/buildkitx:buildx-stable-1@sha256:" + digest,
		"xmoby/buildkit:buildx-stable-1@sha256:" + digest,
		"moby/buildkit:@sha256:" + digest,
		"moby/buildkit:.tag@sha256:" + digest,
		"moby/buildkit:-tag@sha256:" + digest,
		"moby/buildkit:buildx-stable-1",
		"moby/buildkit@sha256:" + digest,
		"moby/buildkit:buildx-stable-1@sha256:" + strings.Repeat("A", 64),
		"moby/buildkit:buildx-stable-1@sha512:" + digest,
		"moby/buildkit:tag:x@sha256:" + digest,
		"moby/buildkit:täg@sha256:" + digest,
		"tonistiigi/binfmt:qemu@sha256:" + digest + "\n",
	}
	scratch := t.TempDir()
	fields := base.fields()
	for at, field := range fields {
		for index, candidate := range candidates {
			if strings.ContainsAny(candidate, "\"\n") {
				// A quote or a newline cannot sit inside one key = "value"
				// line; both parsers reject such a line on its own.
				continue
			}
			var policy strings.Builder
			policy.WriteString("[tool_pins]\n")
			for other, row := range fields {
				value := *row.dst
				if other == at {
					value = candidate
				}
				policy.WriteString(row.key + ` = "` + value + "\"\n")
			}
			path := filepath.Join(scratch, field.key+"-"+string(rune('a'+index%26))+string(rune('a'+index/26))+".toml")
			if err := os.WriteFile(path, []byte(policy.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			_, goErr := parseToolPins(policy.String(), path)
			_, loaderErr := runToolPinLoader(loader, path)
			if (goErr == nil) != (loaderErr == nil) {
				t.Errorf("%s = %q: Go parse error = %v, loader error = %v; the two must agree", field.key, candidate, goErr, loaderErr)
			}
			if goErr == nil && !field.format.MatchString(candidate) {
				t.Errorf("%s = %q: Go parse accepted a value its format rejects", field.key, candidate)
			}
		}
	}
}
