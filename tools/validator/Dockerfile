ARG VALIDATOR_BASE_IMAGE=node:24-trixie-slim@sha256:366fdef91728b1b7fa18c84fba63b6e79ed77b7e10cc206878e9705da4d7b169
ARG GO_VERSION=1.26.5
ARG GO_LINUX_X86_64_SHA256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
ARG GO_LINUX_ARM64_SHA256=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49
ARG HADOLINT_VERSION=v2.14.0
ARG HADOLINT_LINUX_X86_64_SHA256=6bf226944684f56c84dd014e8b979d27425c0148f61b3bd99bcc6f39e9dc5a47
ARG HADOLINT_LINUX_ARM64_SHA256=331f1d3511b84a4f1e3d18d52fec284723e4019552f4f47b19322a53ce9a40ed
ARG DEADCODE_VERSION=v0.38.0
ARG MARKDOWNLINT_VERSION=0.49.0
ARG RUST_VERSION=1.97.0
ARG RUSTUP_VERSION=1.29.0
ARG RUSTUP_INIT_LINUX_X86_64_SHA256=4acc9acc76d5079515b46346a485974457b5a79893cfb01112423c89aeb5aa10
ARG RUSTUP_INIT_LINUX_ARM64_SHA256=9732d6c5e2a098d3521fca8145d826ae0aaa067ef2385ead08e6feac88fa5792

FROM ${VALIDATOR_BASE_IMAGE}

COPY tools/markdownlint/package.json tools/markdownlint/package-lock.json /usr/local/lib/workcell-markdownlint/
COPY --chmod=0444 runtime/container/debian-bootstrap.env /usr/local/share/workcell/debian-bootstrap.env

ARG GO_VERSION
ARG GO_LINUX_X86_64_SHA256
ARG GO_LINUX_ARM64_SHA256
ARG HADOLINT_VERSION
ARG HADOLINT_LINUX_X86_64_SHA256
ARG HADOLINT_LINUX_ARM64_SHA256
ARG DEADCODE_VERSION
ARG MARKDOWNLINT_VERSION
ARG RUST_VERSION
ARG RUSTUP_VERSION
ARG RUSTUP_INIT_LINUX_X86_64_SHA256
ARG RUSTUP_INIT_LINUX_ARM64_SHA256

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/cargo/bin:/usr/local/go/bin:${PATH}
ENV RUSTUP_HOME=/usr/local/rustup

# hadolint ignore=DL3008
RUN mapfile -t debian_bootstrap_pins < /usr/local/share/workcell/debian-bootstrap.env \
  && [[ "${#debian_bootstrap_pins[@]}" -eq 7 ]] \
  && [[ "${debian_bootstrap_pins[0]}" =~ ^DEBIAN_SNAPSHOT=[0-9]{8}T[0-9]{6}Z$ ]] \
  && [[ "${debian_bootstrap_pins[1]}" =~ ^DEBIAN_OPENSSL_AMD64_PATH=pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_amd64\.deb$ ]] \
  && [[ "${debian_bootstrap_pins[2]}" =~ ^DEBIAN_OPENSSL_AMD64_SHA256=[0-9a-f]{64}$ ]] \
  && [[ "${debian_bootstrap_pins[3]}" =~ ^DEBIAN_OPENSSL_ARM64_PATH=pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_arm64\.deb$ ]] \
  && [[ "${debian_bootstrap_pins[4]}" =~ ^DEBIAN_OPENSSL_ARM64_SHA256=[0-9a-f]{64}$ ]] \
  && [[ "${debian_bootstrap_pins[5]}" =~ ^DEBIAN_CA_CERTIFICATES_PATH=pool/main/c/ca-certificates/ca-certificates_[A-Za-z0-9.+~_-]+_all\.deb$ ]] \
  && [[ "${debian_bootstrap_pins[6]}" =~ ^DEBIAN_CA_CERTIFICATES_SHA256=[0-9a-f]{64}$ ]] \
  && DEBIAN_SNAPSHOT="${debian_bootstrap_pins[0]#*=}" \
  && DEBIAN_OPENSSL_AMD64_PATH="${debian_bootstrap_pins[1]#*=}" \
  && DEBIAN_OPENSSL_AMD64_SHA256="${debian_bootstrap_pins[2]#*=}" \
  && DEBIAN_OPENSSL_ARM64_PATH="${debian_bootstrap_pins[3]#*=}" \
  && DEBIAN_OPENSSL_ARM64_SHA256="${debian_bootstrap_pins[4]#*=}" \
  && DEBIAN_CA_CERTIFICATES_PATH="${debian_bootstrap_pins[5]#*=}" \
  && DEBIAN_CA_CERTIFICATES_SHA256="${debian_bootstrap_pins[6]#*=}" \
  && [[ "${DEBIAN_OPENSSL_AMD64_PATH%_amd64.deb}" == "${DEBIAN_OPENSSL_ARM64_PATH%_arm64.deb}" ]] \
  && unset debian_bootstrap_pins \
  && fetch_snapshot_bootstrap_package() { \
      local path="$1"; \
      local output="$2"; \
      local attempt="" host=""; \
      for attempt in 1 2 3 4 5 6 7 8; do \
        if [[ $((attempt % 2)) -eq 1 ]]; then host="snapshot-cloudflare.debian.org"; else host="snapshot.debian.org"; fi; \
        if node --dns-result-order=ipv4first -e 'const fs = require("fs"); (async () => { const [url, output] = process.argv.slice(1); const response = await fetch(url); if (!response.ok) { throw new Error("unexpected status " + response.status + " for " + url); } const file = fs.createWriteStream(output); for await (const chunk of response.body) { file.write(chunk); } file.end(); await new Promise((resolve, reject) => { file.on("finish", resolve); file.on("error", reject); }); })().catch((error) => { console.error(error.stack || error); process.exit(1); });' "https://${host}/${path}" "${output}"; then \
          return 0; \
        fi; \
        rm -f "${output}"; \
        if [[ "${attempt}" -eq 8 ]]; then \
          return 1; \
        fi; \
        sleep "$((attempt * 5))"; \
      done; \
    } \
  && bootstrap_snapshot_tls() { \
       local openssl_path="" openssl_url="" openssl_sha256="" ca_url="" ca_sha256=""; \
       case "$(dpkg --print-architecture)" in \
         amd64) \
           openssl_path="${DEBIAN_OPENSSL_AMD64_PATH}"; \
           openssl_sha256="${DEBIAN_OPENSSL_AMD64_SHA256}"; \
           ;; \
         arm64) \
           openssl_path="${DEBIAN_OPENSSL_ARM64_PATH}"; \
           openssl_sha256="${DEBIAN_OPENSSL_ARM64_SHA256}"; \
           ;; \
         *) \
           echo "Unsupported dpkg architecture for snapshot TLS bootstrap: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
       openssl_url="archive/debian/${DEBIAN_SNAPSHOT}/${openssl_path}"; \
       ca_url="archive/debian/${DEBIAN_SNAPSHOT}/${DEBIAN_CA_CERTIFICATES_PATH}"; \
       ca_sha256="${DEBIAN_CA_CERTIFICATES_SHA256}"; \
       fetch_snapshot_bootstrap_package "${openssl_url}" /tmp/workcell-bootstrap-openssl.deb \
       && echo "${openssl_sha256}  /tmp/workcell-bootstrap-openssl.deb" | sha256sum -c - \
       && fetch_snapshot_bootstrap_package "${ca_url}" /tmp/workcell-bootstrap-ca-certificates.deb \
       && echo "${ca_sha256}  /tmp/workcell-bootstrap-ca-certificates.deb" | sha256sum -c - \
       && dpkg -i /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb \
       || return 1; \
       rm -f /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb; \
     } \
  && bootstrap_snapshot_tls \
  && rm -f /etc/apt/sources.list.d/debian.sources \
  && set_snapshot_sources() { \
       local host="$1"; \
       printf 'deb [check-valid-until=no] https://%s/archive/debian/%s trixie main\n' "${host}" "${DEBIAN_SNAPSHOT}" > /etc/apt/sources.list; \
       printf 'deb [check-valid-until=no] https://%s/archive/debian/%s trixie-updates main\n' "${host}" "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list; \
       printf 'deb [check-valid-until=no] https://%s/archive/debian-security/%s trixie-security main\n' "${host}" "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list; \
     } \
  && printf 'Acquire::Check-Valid-Until "false";\nAcquire::Retries "5";\nAcquire::http::Timeout "30";\nAcquire::https::Timeout "30";\n' > /etc/apt/apt.conf.d/99workcell-validator-snapshot \
  && install_validator_packages() { \
       DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
         ca-certificates \
         codespell \
         curl \
         gcc \
         git \
         groff-base \
         jq \
         libc6-dev \
         llvm \
         mandoc \
         openssh-client \
         procps \
         shellcheck \
         shfmt \
         yamllint \
       && return 0; \
     } \
  && for attempt in 1 2 3; do \
       rm -rf /var/lib/apt/lists/*; \
       if [[ $((attempt % 2)) -eq 1 ]]; then set_snapshot_sources snapshot-cloudflare.debian.org; else set_snapshot_sources snapshot.debian.org; fi; \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && install_validator_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 3 ]]; then \
         exit 1; \
       fi; \
       sleep "$((attempt * 5))"; \
     done \
  && go_archive="" \
  && go_sha256="" \
  && rustup_target="" \
  && rustup_sha256="" \
  && hadolint_asset="" \
  && hadolint_sha256="" \
  && case "$(dpkg --print-architecture)" in \
       amd64) \
         go_archive="go${GO_VERSION}.linux-amd64.tar.gz"; \
         go_sha256="${GO_LINUX_X86_64_SHA256}"; \
         hadolint_asset="hadolint-linux-x86_64"; \
         hadolint_sha256="${HADOLINT_LINUX_X86_64_SHA256}"; \
         rustup_target="x86_64-unknown-linux-gnu"; \
         rustup_sha256="${RUSTUP_INIT_LINUX_X86_64_SHA256}"; \
         ;; \
       arm64) \
         go_archive="go${GO_VERSION}.linux-arm64.tar.gz"; \
         go_sha256="${GO_LINUX_ARM64_SHA256}"; \
         hadolint_asset="hadolint-linux-arm64"; \
         hadolint_sha256="${HADOLINT_LINUX_ARM64_SHA256}"; \
         rustup_target="aarch64-unknown-linux-gnu"; \
         rustup_sha256="${RUSTUP_INIT_LINUX_ARM64_SHA256}"; \
         ;; \
       *) \
         echo "Unsupported dpkg architecture for hadolint: $(dpkg --print-architecture)" >&2; \
         exit 1; \
         ;; \
     esac \
  && curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    "https://go.dev/dl/${go_archive}" \
    -o "/tmp/${go_archive}" \
  && echo "${go_sha256}  /tmp/${go_archive}" | sha256sum -c - \
  && rm -rf /usr/local/go \
  && tar -C /usr/local -xzf "/tmp/${go_archive}" \
  && curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/${rustup_target}/rustup-init" \
    -o /tmp/rustup-init \
  && echo "${rustup_sha256}  /tmp/rustup-init" | sha256sum -c - \
  && chmod 0755 /tmp/rustup-init \
  && /tmp/rustup-init -y --profile minimal --default-toolchain "${RUST_VERSION}" --component rustfmt --component clippy --no-modify-path \
  && ln -sf /usr/local/go/bin/go /usr/local/bin/go \
  && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
  && ln -sf /usr/local/cargo/bin/cargo /usr/local/bin/cargo \
  && ln -sf /usr/local/cargo/bin/rustc /usr/local/bin/rustc \
  && ln -sf /usr/local/cargo/bin/rustfmt /usr/local/bin/rustfmt \
  && ln -sf /usr/local/cargo/bin/cargo-fmt /usr/local/bin/cargo-fmt \
  && ln -sf /usr/local/cargo/bin/cargo-clippy /usr/local/bin/cargo-clippy \
  && curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    "https://github.com/hadolint/hadolint/releases/download/${HADOLINT_VERSION}/${hadolint_asset}" \
    -o /usr/local/bin/hadolint \
  && echo "${hadolint_sha256}  /usr/local/bin/hadolint" | sha256sum -c - \
  && chmod 0755 /usr/local/bin/hadolint \
  && GOBIN=/usr/local/bin go install "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" \
  && npm ci --prefix /usr/local/lib/workcell-markdownlint --ignore-scripts --omit=dev \
  && ln -sf /usr/local/lib/workcell-markdownlint/node_modules/.bin/markdownlint /usr/local/bin/markdownlint \
  && go version | grep -F "go version go${GO_VERSION} " >/dev/null \
  && deadcode -h >/dev/null \
  && rustc --version | grep -F "rustc ${RUST_VERSION} " >/dev/null \
  && cargo fmt --version >/dev/null \
  && cargo clippy --version >/dev/null \
  && hadolint --version >/dev/null \
  && markdownlint --version | grep -F "${MARKDOWNLINT_VERSION}" >/dev/null \
  && rm -f /tmp/rustup-init "/tmp/${go_archive}" \
  && rm -rf \
    /usr/local/rustup/downloads \
    /usr/local/rustup/tmp \
    /var/lib/apt/lists/* \
    /var/cache/apt/* \
    /var/cache/debconf/*-old \
    /var/cache/ldconfig/aux-cache \
  && groupadd --system --gid 65532 workcell \
  && useradd --system --uid 65532 --gid 65532 --home-dir /home/workcell --shell /bin/bash workcell \
  && install -d -o 65532 -g 65532 -m 0700 \
    /home/workcell \
    /home/workcell/.cache \
    /home/workcell/.cache/go-build \
    /home/workcell/.cache/go-mod \
    /home/workcell/.cache/cargo-target \
    /home/workcell/.tmp

WORKDIR /workspace

ENV HOME=/home/workcell
ENV XDG_CACHE_HOME=/home/workcell/.cache
ENV GOCACHE=/home/workcell/.cache/go-build
ENV GOMODCACHE=/home/workcell/.cache/go-mod
ENV CARGO_TARGET_DIR=/home/workcell/.cache/cargo-target
ENV TMPDIR=/home/workcell/.tmp
ENV WORKCELL_REQUIRE_SUPPORTED_COVERAGE=1
ENV WORKCELL_REQUIRE_DOCKERFILE_LINT=1

USER workcell

ENTRYPOINT ["/bin/bash", "-lc"]
