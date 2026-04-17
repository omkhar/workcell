ARG VALIDATOR_BASE_IMAGE=node:22-trixie-slim@sha256:76043ed3132293c26b960ede4358d3c8ba424ee64662cd2d56318b76fcc51c4c
ARG DEBIAN_SNAPSHOT=20260417T000000Z
ARG GO_VERSION=1.26.2
ARG GO_LINUX_X86_64_SHA256=990e6b4bbba816dc3ee129eaeaf4b42f17c2800b88a2166c265ac1a200262282
ARG GO_LINUX_ARM64_SHA256=c958a1fe1b361391db163a485e21f5f228142d6f8b584f6bef89b26f66dc5b23
ARG HADOLINT_VERSION=v2.14.0
ARG HADOLINT_LINUX_X86_64_SHA256=6bf226944684f56c84dd014e8b979d27425c0148f61b3bd99bcc6f39e9dc5a47
ARG HADOLINT_LINUX_ARM64_SHA256=331f1d3511b84a4f1e3d18d52fec284723e4019552f4f47b19322a53ce9a40ed
ARG MARKDOWNLINT_VERSION=0.48.0
ARG RUST_VERSION=1.95.0
ARG RUSTUP_VERSION=1.29.0
ARG RUSTUP_INIT_LINUX_X86_64_SHA256=4acc9acc76d5079515b46346a485974457b5a79893cfb01112423c89aeb5aa10
ARG RUSTUP_INIT_LINUX_ARM64_SHA256=9732d6c5e2a098d3521fca8145d826ae0aaa067ef2385ead08e6feac88fa5792

FROM ${VALIDATOR_BASE_IMAGE}

COPY tools/markdownlint/package.json tools/markdownlint/package-lock.json /usr/local/lib/workcell-markdownlint/

ARG DEBIAN_SNAPSHOT
ARG GO_VERSION
ARG GO_LINUX_X86_64_SHA256
ARG GO_LINUX_ARM64_SHA256
ARG HADOLINT_VERSION
ARG HADOLINT_LINUX_X86_64_SHA256
ARG HADOLINT_LINUX_ARM64_SHA256
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
RUN fetch_snapshot_bootstrap_package() { \
      local url="$1"; \
      local output="$2"; \
      node -e 'const fs = require("fs"); (async () => { const [url, output] = process.argv.slice(1); const response = await fetch(url); if (!response.ok) { throw new Error(`unexpected status ${response.status} for ${url}`); } const file = fs.createWriteStream(output); for await (const chunk of response.body) { file.write(chunk); } file.end(); await new Promise((resolve, reject) => { file.on("finish", resolve); file.on("error", reject); }); })().catch((error) => { console.error(error.stack || error); process.exit(1); });' "${url}" "${output}"; \
    } \
  && bootstrap_snapshot_tls() { \
       local openssl_url="" openssl_sha256="" ca_url="" ca_sha256=""; \
       case "$(dpkg --print-architecture)" in \
         amd64) \
           openssl_url="https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb"; \
           openssl_sha256="233d6d1f6ff00509cba554365e7cfb3be36db1dea57291eeb248f2c04d62719d"; \
           ;; \
         arm64) \
           openssl_url="https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb"; \
           openssl_sha256="92dfcdc213cd7a5e6aa34a1ac9660a59e56838d12876f4b34599f8fea39cb432"; \
           ;; \
         *) \
           echo "Unsupported dpkg architecture for snapshot TLS bootstrap: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
       ca_url="https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/pool/main/c/ca-certificates/ca-certificates_20250419_all.deb"; \
       ca_sha256="ef590f89563aa4b46c8260d49d1cea0fc1b181d19e8df3782694706adf05c184"; \
       fetch_snapshot_bootstrap_package "${openssl_url}" /tmp/workcell-bootstrap-openssl.deb; \
       echo "${openssl_sha256}  /tmp/workcell-bootstrap-openssl.deb" | sha256sum -c -; \
       fetch_snapshot_bootstrap_package "${ca_url}" /tmp/workcell-bootstrap-ca-certificates.deb; \
       echo "${ca_sha256}  /tmp/workcell-bootstrap-ca-certificates.deb" | sha256sum -c -; \
       dpkg -i /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb; \
       rm -f /tmp/workcell-bootstrap-openssl.deb /tmp/workcell-bootstrap-ca-certificates.deb; \
     } \
  && bootstrap_snapshot_tls \
  && rm -f /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/%s trixie main\n' "${DEBIAN_SNAPSHOT}" > /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/%s trixie-updates main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/%s trixie-security main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
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
  && npm ci --prefix /usr/local/lib/workcell-markdownlint --ignore-scripts --omit=dev \
  && ln -sf /usr/local/lib/workcell-markdownlint/node_modules/.bin/markdownlint /usr/local/bin/markdownlint \
  && go version | grep -F "go version go${GO_VERSION} " >/dev/null \
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
