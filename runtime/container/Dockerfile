# Node.js LTS base image (v24 trixie-slim). Pin includes digest for reproducibility.
# To update: fetch new digest with `docker pull node:24-trixie-slim` and update both
# the tag and @sha256: digest. Then re-run verify-reproducible-build.sh.
ARG NODE_BASE_IMAGE=node:24-trixie-slim@sha256:366fdef91728b1b7fa18c84fba63b6e79ed77b7e10cc206878e9705da4d7b169

# Unix epoch timestamp for reproducible build artifact normalization.
# Set to 0 to use epoch; set to a specific date for release builds.
ARG SOURCE_DATE_EPOCH=0

# Debian snapshot date and TLS-bootstrap packages are one reproducibility tuple.
# Resolve the snapshot plus both architecture paths and all digests together,
# update both Dockerfiles, and then re-run verify-reproducible-build.sh.
ARG DEBIAN_SNAPSHOT=20260721T000000Z
ARG RUST_TOOLCHAIN_IMAGE=rust:1.97.0-slim-trixie@sha256:14c4fe50ea427dc42381a1a09a9a839c1d2346a2e508cd491bf02c659dbc0ed7

FROM ${RUST_TOOLCHAIN_IMAGE} AS rust-toolchain

FROM ${NODE_BASE_IMAGE} AS runtime-base

ARG SOURCE_DATE_EPOCH
ARG DEBIAN_SNAPSHOT

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
ENV TZ=UTC
ENV npm_config_audit=false
ENV npm_config_fund=false
ENV npm_config_update_notifier=false

# hadolint ignore=DL3008
RUN fetch_snapshot_bootstrap_package() { \
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
       local openssl_url="" openssl_sha256="" ca_url="" ca_sha256=""; \
       case "$(dpkg --print-architecture)" in \
         amd64) \
           openssl_url="archive/debian/${DEBIAN_SNAPSHOT}/pool/main/o/openssl/openssl_3.5.6-1~deb13u2_amd64.deb"; \
           openssl_sha256="7b4270966255f0bad468ac889f4b090a6e10eb6ccbc2be0fa418560b24bdef5a"; \
           ;; \
         arm64) \
           openssl_url="archive/debian/${DEBIAN_SNAPSHOT}/pool/main/o/openssl/openssl_3.5.6-1~deb13u2_arm64.deb"; \
           openssl_sha256="1ee735e2480519bd1fd5f6576a879243bc81b9773059e1fa1ec8b8943c84e77c"; \
           ;; \
         *) \
           echo "Unsupported dpkg architecture for snapshot TLS bootstrap: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
       ca_url="archive/debian/${DEBIAN_SNAPSHOT}/pool/main/c/ca-certificates/ca-certificates_20250419_all.deb"; \
       ca_sha256="ef590f89563aa4b46c8260d49d1cea0fc1b181d19e8df3782694706adf05c184"; \
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
  && printf 'Acquire::Check-Valid-Until "false";\nAcquire::Retries "5";\nAcquire::http::Timeout "30";\nAcquire::https::Timeout "30";\n' > /etc/apt/apt.conf.d/99workcell-snapshot \
  && apt_indexes_have_runtime_packages() { \
       apt-cache show --no-all-versions \
       bash \
       bubblewrap \
       ca-certificates \
       curl \
       fd-find \
       git \
       jq \
       less \
       openssh-client \
       passwd \
       procps \
       ripgrep \
       sudo \
       unzip \
       util-linux \
       xz-utils >/dev/null; \
     } \
  && install_runtime_packages() { \
       DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       bash \
       bubblewrap \
       ca-certificates \
       curl \
       fd-find \
       git \
       jq \
       less \
       openssh-client \
       passwd \
       procps \
       ripgrep \
       sudo \
       unzip \
       util-linux \
       xz-utils \
       && return 0; \
     } \
  && for attempt in 1 2 3 4 5; do \
       if [[ $((attempt % 2)) -eq 1 ]]; then set_snapshot_sources snapshot-cloudflare.debian.org; else set_snapshot_sources snapshot.debian.org; fi; \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && apt_indexes_have_runtime_packages \
         && install_runtime_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 5 ]]; then \
         exit 1; \
       fi; \
       rm -rf /var/lib/apt/lists/*; \
       sleep "$((attempt * 5))"; \
     done \
  && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
  && groupadd --system --gid 65532 workcell \
  && useradd --system --uid 65532 --gid 65532 --home-dir /state/agent-home --shell /usr/sbin/nologin workcell \
  && mkdir -p \
    /etc/workcell/empty-xdg \
    /opt/workcell/host-injections \
    /opt/workcell/host-inputs \
    /opt/workcell/workspace-control-plane \
    /state/agent-home \
    /usr/local/libexec/workcell \
    /usr/local/libexec/workcell/core \
    /usr/local/libexec/workcell/real \
    /workspace \
  && chmod 0555 /etc/workcell/empty-xdg \
  && chown -R 65532:65532 /state \
  && chmod 0755 /state \
  && chmod 0700 /state/agent-home \
  && install -d -o 65532 -g 65532 -m 1777 /state/tmp \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache \
  && find /var/log -type f -delete \
  && find \
      /etc/apt \
      /etc/workcell \
      /opt/workcell \
      /state \
      /usr/local/bin \
      /usr/local/lib \
      /usr/local/libexec \
      /usr/share \
      /var \
      \( \
        -path '/etc/ssl' -o \
        -path '/var/lib/apt/lists' -o \
        -path '/var/cache/apt' \
      \) -prune -o \
      \( -path '/etc/hostname' -o -path '/etc/hosts' -o -path '/etc/resolv.conf' \) -prune -o \
      -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"

FROM runtime-base AS provider-builder

ARG SOURCE_DATE_EPOCH

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
WORKDIR /opt/workcell/providers

COPY runtime/container/providers /opt/workcell/providers

RUN install_provider_packages() { \
       npm ci --omit=dev --ignore-scripts --no-audit --no-fund \
       && return 0; \
     } \
  && for attempt in 1 2 3; do \
       rm -rf /opt/workcell/providers/node_modules; \
       if install_provider_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 3 ]]; then \
         exit 1; \
       fi; \
       npm cache clean --force >/dev/null 2>&1 || true; \
       sleep "$((attempt * 5))"; \
     done \
  && find /opt/workcell/providers -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"

FROM runtime-base AS runtime-builder

# Anthropic Claude Code native binary version and SHA256 checksums for each
# Linux architecture. Workcell pins the stable channel with the cool-off in
# policy/provider-bumps.toml. To update manually, read the stable release set
# from the public release bucket, verify the per-platform checksums in the
# corresponding manifest.json, update CLAUDE_VERSION, and refresh the
# CLAUDE_SHA256 values below.
ARG CLAUDE_VERSION=2.1.207
# OpenAI Codex CLI version and SHA256 checksums for each architecture.
# Workcell follows the stable npm channel and matching non-prerelease Rust
# release after the cool-off in policy/provider-bumps.toml. The runtime
# consumes the statically linked musl Linux tarballs (which carry sigstore
# bundles); to update manually, resolve the eligible stable version, verify
# the GitHub release asset digests for both musl Linux tarballs, and refresh
# the CODEX_SHA256 values for arm64 and amd64 below.
ARG CODEX_VERSION=0.144.1
# GitHub Copilot CLI native binary version and SHA256 checksums for each Linux
# architecture. Workcell pins the non-prerelease GitHub release asset and keeps
# token auth explicit through copilot_github_token instead of host GitHub CLI
# state.
ARG COPILOT_VERSION=1.0.70
ARG CODEX_RELEASE_URL=
ARG COPILOT_RELEASE_URL=
ARG RUST_VERSION=1.97.0
ARG TARGETARCH
ARG BUILDKIT_MULTI_PLATFORM=1
ARG SOURCE_DATE_EPOCH

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
WORKDIR /workcell-rust

ENV CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/cargo/bin:${PATH}
ENV RUSTUP_HOME=/usr/local/rustup

COPY --from=rust-toolchain /usr/local/cargo /usr/local/cargo
COPY --from=rust-toolchain /usr/local/rustup /usr/local/rustup

# hadolint ignore=DL3008
RUN apt_indexes_have_runtime_builder_packages() { \
      apt-cache show --no-all-versions \
      gcc \
      libc6-dev >/dev/null; \
    } \
  && install_runtime_builder_packages() { \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      gcc \
      libc6-dev \
      && return 0; \
    } \
  && for attempt in 1 2 3 4 5; do \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && apt_indexes_have_runtime_builder_packages \
         && install_runtime_builder_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 5 ]]; then \
         exit 1; \
       fi; \
       rm -rf /var/lib/apt/lists/*; \
       sleep "$((attempt * 5))"; \
     done \
  && toolchain_target="" \
  && case "$(dpkg --print-architecture)" in \
       amd64) \
         toolchain_target="x86_64-unknown-linux-gnu"; \
         ;; \
       arm64) \
         toolchain_target="aarch64-unknown-linux-gnu"; \
         ;; \
       *) \
         echo "Unsupported dpkg architecture for Rust toolchain copy: $(dpkg --print-architecture)" >&2; \
         exit 1; \
         ;; \
     esac \
  && toolchain_bin="${RUSTUP_HOME}/toolchains/${RUST_VERSION}-${toolchain_target}/bin" \
  && "${toolchain_bin}/rustc" --version | grep -F "rustc ${RUST_VERSION} " >/dev/null \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache \
  && rm -rf /usr/local/rustup/downloads /usr/local/rustup/tmp

RUN TARGET_ARCH="${TARGETARCH:-}" \
  && if [[ -z "${TARGET_ARCH}" ]]; then \
       case "$(dpkg --print-architecture)" in \
         arm64) TARGET_ARCH="arm64" ;; \
         amd64) TARGET_ARCH="amd64" ;; \
         *) \
           echo "Unsupported dpkg architecture: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
     fi \
  && case "${TARGET_ARCH}" in \
    arm64) \
      CLAUDE_PLATFORM="linux-arm64"; \
      CLAUDE_SHA256="8bc14a284065383460f37981d724b8f7aa7ca93c9849d2fe367e08f03383f454"; \
      ;; \
    amd64) \
      CLAUDE_PLATFORM="linux-x64"; \
      CLAUDE_SHA256="85e7e988a392d859f90802ca21fb26e89d3c9ab527f5ed0b08df3955e34d5c83"; \
      ;; \
    *) \
      echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; \
      exit 1; \
      ;; \
  esac \
  && curl --ipv4 -fsSL "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/${CLAUDE_VERSION}/${CLAUDE_PLATFORM}/claude" \
    --retry 8 \
    --retry-all-errors \
    --retry-delay 5 \
    --connect-timeout 30 \
    -o /tmp/claude \
  && echo "${CLAUDE_SHA256}  /tmp/claude" | sha256sum -c - \
  && install -m 0755 /tmp/claude /usr/local/libexec/workcell/real/claude \
  && touch -d "@${SOURCE_DATE_EPOCH}" /usr/local/libexec/workcell/real/claude \
  && rm -f /tmp/claude

RUN TARGET_ARCH="${TARGETARCH:-}" \
  && if [[ -z "${TARGET_ARCH}" ]]; then \
       case "$(dpkg --print-architecture)" in \
         arm64) TARGET_ARCH="arm64" ;; \
         amd64) TARGET_ARCH="amd64" ;; \
         *) \
           echo "Unsupported dpkg architecture: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
     fi \
  && case "${TARGET_ARCH}" in \
    arm64) \
      CODEX_ARCH="aarch64-unknown-linux-musl"; \
      CODEX_SHA256="b9f8ef5f98e46ced4dbbd3756a4223e3ee299a457ff488a3305bea455da8b5b8"; \
      ;; \
    amd64) \
      CODEX_ARCH="x86_64-unknown-linux-musl"; \
      CODEX_SHA256="84091ae20c65fcc7d4120db97d1bd57d7ff8df9c7609fb781c78c2ebbd4f5a28"; \
      ;; \
    *) \
      echo "Unsupported TARGETARCH: ${TARGET_ARCH}" >&2; \
      exit 1; \
      ;; \
  esac \
  && if [[ -n "${CODEX_RELEASE_URL}" ]]; then \
       curl --ipv4 -fsSL "${CODEX_RELEASE_URL}" \
         --retry 8 \
         --retry-all-errors \
         --retry-delay 5 \
         --connect-timeout 30 \
         -o /tmp/codex.tar.gz; \
     else \
       curl --ipv4 -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-${CODEX_ARCH}.tar.gz" \
         --retry 8 \
         --retry-all-errors \
         --retry-delay 5 \
         --connect-timeout 30 \
         -o /tmp/codex.tar.gz; \
     fi \
  && echo "${CODEX_SHA256}  /tmp/codex.tar.gz" | sha256sum -c - \
  && tar -xzf /tmp/codex.tar.gz -C /tmp \
  && mv "/tmp/codex-${CODEX_ARCH}" /usr/local/libexec/workcell/real/codex \
  && chmod 0755 /usr/local/libexec/workcell/real/codex \
  && touch -d "@${SOURCE_DATE_EPOCH}" /usr/local/libexec/workcell/real/codex \
  && rm -f /tmp/codex.tar.gz

RUN TARGET_ARCH="${TARGETARCH:-}" \
  && if [[ -z "${TARGET_ARCH}" ]]; then \
       case "$(dpkg --print-architecture)" in \
         arm64) TARGET_ARCH="arm64" ;; \
         amd64) TARGET_ARCH="amd64" ;; \
         *) \
           echo "Unsupported dpkg architecture: $(dpkg --print-architecture)" >&2; \
           exit 1; \
           ;; \
       esac; \
     fi \
  && case "${TARGET_ARCH}" in \
    arm64) \
      COPILOT_PLATFORM="linux-arm64"; \
      COPILOT_SHA256="1cb358a1a8ac8d0f680b54c6eac990c376043314409a06a5aa4fed0e0a7d3362"; \
      ;; \
    amd64) \
      COPILOT_PLATFORM="linux-x64"; \
      COPILOT_SHA256="4edee3cd005254960789329181968b209b17cab47f43ee13c9e071b1f7e33095"; \
      ;; \
    *) \
      echo "Unsupported TARGETARCH: ${TARGET_ARCH}" >&2; \
      exit 1; \
      ;; \
  esac \
  && if [[ -n "${COPILOT_RELEASE_URL}" ]]; then \
       curl --ipv4 -fsSL "${COPILOT_RELEASE_URL}" \
         --retry 8 \
         --retry-all-errors \
         --retry-delay 5 \
         --connect-timeout 30 \
         -o /tmp/copilot.tar.gz; \
     else \
       curl --ipv4 -fsSL "https://github.com/github/copilot-cli/releases/download/v${COPILOT_VERSION}/copilot-${COPILOT_PLATFORM}.tar.gz" \
         --retry 8 \
         --retry-all-errors \
         --retry-delay 5 \
         --connect-timeout 30 \
         -o /tmp/copilot.tar.gz; \
     fi \
  && echo "${COPILOT_SHA256}  /tmp/copilot.tar.gz" | sha256sum -c - \
  && tar -xzf /tmp/copilot.tar.gz -C /tmp copilot \
  && install -m 0755 /tmp/copilot /usr/local/libexec/workcell/real/copilot \
  && touch -d "@${SOURCE_DATE_EPOCH}" /usr/local/libexec/workcell/real/copilot \
  && rm -f /tmp/copilot.tar.gz /tmp/copilot

# Copy ONLY the Rust source before the expensive compile so that changes to
# adapters or shell scripts do not invalidate the cargo build cache layer.
COPY runtime/container/rust /workcell-rust

RUN export \
      SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
      CARGO_HOME=/workcell-rust/cargo-home \
      CARGO_TARGET_DIR=/tmp/workcell-rust-target \
  && toolchain_target="" \
  && case "$(dpkg --print-architecture)" in \
       amd64) \
         toolchain_target="x86_64-unknown-linux-gnu"; \
         ;; \
       arm64) \
         toolchain_target="aarch64-unknown-linux-gnu"; \
         ;; \
       *) \
         echo "Unsupported dpkg architecture for offline cargo build: $(dpkg --print-architecture)" >&2; \
         exit 1; \
         ;; \
     esac \
  && toolchain_bin="${RUSTUP_HOME}/toolchains/${RUST_VERSION}-${toolchain_target}/bin" \
  && RUSTC="${toolchain_bin}/rustc" \
    "${toolchain_bin}/cargo" build \
      --release \
      --locked \
      --offline \
  && install -m 0644 /tmp/workcell-rust-target/release/libworkcell_exec_guard.so /usr/local/lib/libworkcell_exec_guard.so \
  && install -m 0644 /tmp/workcell-rust-target/release/workcell-launcher /usr/local/libexec/workcell/core/launcher \
  # `git` is dispatched by workcell-launcher based on argv[0] (see
  # LAUNCH_TARGETS in src/bin/workcell-launcher.rs).  Hard-link the
  # already-installed launcher under the `git` name instead of building
  # a near-identical second binary; hard-link keeps mode/ownership/perms
  # and survives the layer compression that a symlink would not.
  && ln /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/git \
  && printf '/usr/local/lib/libworkcell_exec_guard.so\n' > /etc/ld.so.preload \
  && rm -rf /tmp/workcell-rust-target /workcell-rust

# Copy adapters and control-plane scripts AFTER cargo build so that changes to
# these frequently-modified files do not invalidate the Rust compilation cache.
COPY adapters /opt/workcell/adapters
COPY runtime/container/bin/git /usr/local/libexec/workcell/git-wrapper.sh
COPY runtime/container/bin/node /usr/local/libexec/workcell/node-wrapper.sh
COPY runtime/container/bin/apt-helper.sh /usr/local/libexec/workcell/apt-helper.sh
COPY runtime/container/bin/apt-wrapper.sh /usr/local/libexec/workcell/apt-wrapper.sh
COPY runtime/container/bin/sudo-wrapper.sh /usr/local/libexec/workcell/sudo-wrapper.sh
COPY runtime/container/assurance.sh /usr/local/libexec/workcell/assurance.sh
COPY runtime/container/apt-broker.sh /usr/local/libexec/workcell/apt-broker.sh
COPY runtime/container/development-wrapper.sh /usr/local/libexec/workcell/development-wrapper.sh
COPY runtime/container/detached-stdin-wrapper.sh /usr/local/libexec/workcell/detached-stdin-wrapper.sh
COPY runtime/container/provider-policy.sh /usr/local/libexec/workcell/provider-policy.sh
COPY runtime/container/home-control-plane.sh /usr/local/libexec/workcell/home-control-plane.sh
COPY runtime/container/provider-wrapper.sh /usr/local/libexec/workcell/provider-wrapper.sh
COPY runtime/container/runtime-user.sh /usr/local/libexec/workcell/runtime-user.sh
COPY runtime/container/public-node-guard.mjs /usr/local/libexec/workcell/public-node-guard.mjs
COPY runtime/container/entrypoint.sh /usr/local/libexec/workcell/entrypoint.sh

RUN mkdir -p /etc/claude-code \
  && cp /opt/workcell/adapters/claude/managed-settings.json /etc/claude-code/managed-settings.json \
  && chmod 0444 \
    /etc/claude-code/managed-settings.json \
  && chmod 0444 \
    /usr/local/libexec/workcell/assurance.sh \
    /usr/local/libexec/workcell/apt-broker.sh \
    /usr/local/libexec/workcell/apt-wrapper.sh \
    /usr/local/libexec/workcell/apt-helper.sh \
    /usr/local/libexec/workcell/detached-stdin-wrapper.sh \
    /usr/local/libexec/workcell/development-wrapper.sh \
    /usr/local/libexec/workcell/entrypoint.sh \
    /usr/local/libexec/workcell/git-wrapper.sh \
    /usr/local/libexec/workcell/node-wrapper.sh \
    /usr/local/libexec/workcell/provider-wrapper.sh \
    /usr/local/libexec/workcell/sudo-wrapper.sh \
    /usr/local/libexec/workcell/runtime-user.sh \
  && chmod +x \
    /usr/local/libexec/workcell/core/launcher \
    /usr/local/libexec/workcell/assurance.sh \
    /usr/local/libexec/workcell/apt-broker.sh \
    /usr/local/libexec/workcell/apt-wrapper.sh \
    /usr/local/libexec/workcell/apt-helper.sh \
    /usr/local/libexec/workcell/detached-stdin-wrapper.sh \
    /usr/local/libexec/workcell/development-wrapper.sh \
    /usr/local/libexec/workcell/home-control-plane.sh \
    /usr/local/libexec/workcell/public-node-guard.mjs \
    /usr/local/libexec/workcell/provider-policy.sh \
    /usr/local/libexec/workcell/runtime-user.sh \
    /usr/local/libexec/workcell/sudo-wrapper.sh \
    /opt/workcell/adapters/claude/hooks/guard-bash.sh \
  && find /opt/workcell/adapters /etc/claude-code /usr/local/libexec/workcell -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}" \
  && touch -h -d "@${SOURCE_DATE_EPOCH}" \
    /etc/ld.so.preload \
    /usr/local/lib/libworkcell_exec_guard.so \
  && rm -rf /tmp/* /var/tmp/* /root/.npm /root/.cache

FROM runtime-base AS runtime

ARG SOURCE_DATE_EPOCH

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Copy provider node_modules directly — extraction at build time avoids
# tar decompression overhead on every container cold start.
COPY --from=provider-builder /opt/workcell/providers /opt/workcell/providers
COPY --from=runtime-builder /etc/claude-code/managed-settings.json /etc/claude-code/managed-settings.json
COPY --from=runtime-builder /etc/ld.so.preload /etc/ld.so.preload
COPY --from=runtime-builder /opt/workcell/adapters /opt/workcell/adapters
COPY --from=runtime-builder /usr/local/lib/libworkcell_exec_guard.so /usr/local/lib/libworkcell_exec_guard.so
COPY --from=runtime-builder /usr/local/libexec/workcell /usr/local/libexec/workcell
COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json

RUN mv /usr/bin/git /usr/local/libexec/workcell/real/git \
  && chmod 0644 /usr/local/libexec/workcell/real/git \
  && mv /usr/local/bin/node /usr/local/libexec/workcell/real/node \
  && chmod 0755 /usr/local/libexec/workcell/real/node \
  && mv /usr/bin/sudo /usr/local/libexec/workcell/real/sudo \
  && chmod 0700 /usr/local/libexec/workcell/real/sudo \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/local/libexec/workcell/git \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/codex \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/claude \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/copilot \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/gemini \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/workcell-entrypoint \
  && ln -sf /usr/local/libexec/workcell/apt-wrapper.sh /usr/local/bin/apt \
  && ln -sf /usr/local/libexec/workcell/apt-wrapper.sh /usr/local/bin/apt-get \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/node \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/codex \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/claude \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/copilot \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/gemini \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/local/bin/git \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/bin/git \
  && ln -sf /usr/local/libexec/workcell/sudo-wrapper.sh /usr/local/bin/sudo \
  && ln -sf /usr/local/libexec/workcell/sudo-wrapper.sh /usr/bin/sudo \
  && chmod 0444 \
    /etc/claude-code/managed-settings.json \
    /usr/local/libexec/workcell/control-plane-manifest.json \
  && chmod 0644 \
    /opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js \
  && rm -f /usr/bin/perlbug /usr/bin/perldoc /usr/bin/perlthanks \
  && chmod +x \
    /usr/local/libexec/workcell/core/git \
    /usr/local/libexec/workcell/core/launcher \
    /usr/local/libexec/workcell/apt-broker.sh \
    /usr/local/libexec/workcell/detached-stdin-wrapper.sh \
    /usr/local/libexec/workcell/home-control-plane.sh \
    /usr/local/libexec/workcell/public-node-guard.mjs \
    /usr/local/libexec/workcell/provider-policy.sh \
    /usr/local/libexec/workcell/sudo-wrapper.sh \
    /usr/local/bin/workcell-entrypoint \
    /opt/workcell/adapters/claude/hooks/guard-bash.sh \
  && rm -rf /tmp/* /var/tmp/* /root/.npm /root/.cache /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache \
  && find \
      /opt/workcell/adapters \
      /etc/claude-code \
      /etc/ld.so.preload \
      /usr/local/bin \
      /usr/local/lib/libworkcell_exec_guard.so \
      /usr/local/libexec/workcell \
      /usr/bin/git \
      /usr/bin/sudo \
      -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"

WORKDIR /workspace

ENV HOME=/state/agent-home
ENV CODEX_HOME=/state/agent-home/.codex
ENV LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
ENV PATH=/usr/local/bin:/usr/bin:/bin

# Defense-in-depth: default to a named unprivileged user so that direct
# `docker run` without `--user root` stays non-root while still having a
# deterministic writable session home and tmp dir. The host launcher passes
# `--user 0:0` explicitly so PID 1 can perform runtime-user setup before
# dropping privileges via setpriv(1).
USER workcell

ENTRYPOINT ["/usr/local/bin/workcell-entrypoint"]
