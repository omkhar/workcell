# Node.js LTS base image (v22 trixie-slim). Pin includes digest for reproducibility.
# To update: fetch new digest with `docker pull node:22-trixie-slim` and update both
# the tag and @sha256: digest. Then re-run verify-reproducible-build.sh.
ARG NODE_BASE_IMAGE=node:22-trixie-slim@sha256:285a78d9fdb7cd2e4a03639cc7bbf609755ce8c0aa4e3b00086211e3e86a7038

# Unix epoch timestamp for reproducible build artifact normalization.
# Set to 0 to use epoch; set to a specific date for release builds.
ARG SOURCE_DATE_EPOCH=0

# Debian snapshot date for apt sources. Pinned for reproducibility.
# To update: choose a newer snapshot date at https://snapshot.debian.org/,
# verify packages are available, update this value, and re-run verify-reproducible-build.sh.
ARG DEBIAN_SNAPSHOT=20260316T000000Z

FROM ${NODE_BASE_IMAGE} AS runtime-base

ARG SOURCE_DATE_EPOCH
ARG DEBIAN_SNAPSHOT

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
ENV TZ=UTC
ENV npm_config_audit=false
ENV npm_config_fund=false
ENV npm_config_update_notifier=false

RUN rm -f /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie main\n' "${DEBIAN_SNAPSHOT}" > /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie-updates main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/%s trixie-security main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'Acquire::Check-Valid-Until "false";\nAcquire::Retries "5";\nAcquire::http::Timeout "30";\n' > /etc/apt/apt.conf.d/99workcell-snapshot \
  && install_runtime_packages() { \
       DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       bash \
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
  && for attempt in 1 2 3; do \
       rm -rf /var/lib/apt/lists/*; \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && install_runtime_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 3 ]]; then \
         exit 1; \
       fi; \
       sleep "$((attempt * 5))"; \
     done \
  && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
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
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache \
  && find /var/log -type f -delete \
  && find / \
    \( \
      -path '/dev' -o -path '/dev/*' -o \
      -path '/proc' -o -path '/proc/*' -o \
      -path '/run' -o -path '/run/*' -o \
      -path '/sys' -o -path '/sys/*' -o \
      -path '/tmp' -o -path '/tmp/*' \
    \) -prune -o \
    \( -path '/etc/hostname' -o -path '/etc/hosts' -o -path '/etc/resolv.conf' \) -prune -o \
    -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"

FROM runtime-base AS provider-builder

ARG SOURCE_DATE_EPOCH

COPY runtime/container/providers /opt/workcell/providers

RUN cd /opt/workcell/providers \
  && npm ci --omit=dev --ignore-scripts --no-audit --no-fund --no-update-notifier \
  && npm cache clean --force \
  && tar --sort=name --mtime="@${SOURCE_DATE_EPOCH}" --owner=0 --group=0 --numeric-owner \
    -cf /tmp/workcell-providers.tar -C /opt/workcell/providers . \
  && cd / \
  && rm -rf /opt/workcell/providers \
  && mkdir -p /opt/workcell/providers \
  && tar -xf /tmp/workcell-providers.tar -C /opt/workcell/providers \
  && rm -f /tmp/workcell-providers.tar \
  && find /opt/workcell/providers -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}" \
  && rm -rf /root/.npm /root/.cache

FROM runtime-base AS runtime-builder

# OpenAI Codex CLI version and SHA256 checksums for each architecture.
# To update: find the new release at https://github.com/openai/codex/releases,
# update CODEX_VERSION, download both architecture tarballs, compute sha256sum,
# and update the CODEX_SHA256 values for arm64 and amd64 below.
ARG CODEX_VERSION=0.117.0-alpha.8
ARG TARGETARCH
ARG BUILDKIT_MULTI_PLATFORM=1
ARG SOURCE_DATE_EPOCH

RUN install_runtime_builder_packages() { \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      cargo \
      rustc \
      && return 0; \
    } \
  && for attempt in 1 2 3; do \
       rm -rf /var/lib/apt/lists/*; \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && install_runtime_builder_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 3 ]]; then \
         exit 1; \
       fi; \
       sleep "$((attempt * 5))"; \
     done \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache

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
      CODEX_ARCH="aarch64-unknown-linux-gnu"; \
      CODEX_SHA256="120b8bd6ececc125198de3fbce896da98a4795b449a4c78d516534e08a63ab14"; \
      ;; \
    amd64) \
      CODEX_ARCH="x86_64-unknown-linux-gnu"; \
      CODEX_SHA256="1d65d8aa80847ac771487d74cb2a63c86ef23479636aa1c1b3d051c9931d314b"; \
      ;; \
    *) \
      echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; \
      exit 1; \
      ;; \
  esac \
  && curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-${CODEX_ARCH}.tar.gz" \
    -o /tmp/codex.tar.gz \
  && echo "${CODEX_SHA256}  /tmp/codex.tar.gz" | sha256sum -c - \
  && tar -xzf /tmp/codex.tar.gz -C /tmp \
  && mv "/tmp/codex-${CODEX_ARCH}" /usr/local/libexec/workcell/real/codex \
  && chmod 0755 /usr/local/libexec/workcell/real/codex \
  && touch -d "@${SOURCE_DATE_EPOCH}" /usr/local/libexec/workcell/real/codex \
  && rm -f /tmp/codex.tar.gz

COPY adapters /opt/workcell/adapters
COPY runtime/container/bin/git /usr/local/libexec/workcell/git-wrapper.sh
COPY runtime/container/bin/node /usr/local/libexec/workcell/node-wrapper.sh
COPY runtime/container/bin/apt-helper.sh /usr/local/libexec/workcell/apt-helper.sh
COPY runtime/container/bin/apt-wrapper.sh /usr/local/libexec/workcell/apt-wrapper.sh
COPY runtime/container/rust /workcell-rust
COPY runtime/container/assurance.sh /usr/local/libexec/workcell/assurance.sh
COPY runtime/container/provider-policy.sh /usr/local/libexec/workcell/provider-policy.sh
COPY runtime/container/home-control-plane.sh /usr/local/libexec/workcell/home-control-plane.sh
COPY runtime/container/provider-wrapper.sh /usr/local/libexec/workcell/provider-wrapper.sh
COPY runtime/container/runtime-user.sh /usr/local/libexec/workcell/runtime-user.sh
COPY runtime/container/public-node-guard.mjs /usr/local/libexec/workcell/public-node-guard.mjs
COPY runtime/container/entrypoint.sh /usr/local/libexec/workcell/entrypoint.sh

RUN cd /workcell-rust \
  && SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    CARGO_HOME=/workcell-rust/cargo-home \
    CARGO_TARGET_DIR=/tmp/workcell-rust-target \
    cargo build \
      --release \
      --locked \
      --offline \
  && install -m 0644 /tmp/workcell-rust-target/release/libworkcell_exec_guard.so /usr/local/lib/libworkcell_exec_guard.so \
  && install -m 0644 /tmp/workcell-rust-target/release/workcell-launcher /usr/local/libexec/workcell/core/launcher \
  && install -m 0644 /tmp/workcell-rust-target/release/workcell-git-launcher /usr/local/libexec/workcell/core/git \
  && mkdir -p /etc/claude-code \
  && cp /opt/workcell/adapters/claude/managed-settings.json /etc/claude-code/managed-settings.json \
  && rm -rf /tmp/workcell-rust-target /workcell-rust \
  && printf '/usr/local/lib/libworkcell_exec_guard.so\n' > /etc/ld.so.preload \
  && chmod 0444 /etc/claude-code/managed-settings.json \
  && chmod 0444 \
    /usr/local/libexec/workcell/assurance.sh \
    /usr/local/libexec/workcell/apt-wrapper.sh \
    /usr/local/libexec/workcell/apt-helper.sh \
    /usr/local/libexec/workcell/entrypoint.sh \
    /usr/local/libexec/workcell/git-wrapper.sh \
    /usr/local/libexec/workcell/node-wrapper.sh \
    /usr/local/libexec/workcell/provider-wrapper.sh \
    /usr/local/libexec/workcell/runtime-user.sh \
  && chmod +x \
    /usr/local/libexec/workcell/core/launcher \
    /usr/local/libexec/workcell/assurance.sh \
    /usr/local/libexec/workcell/apt-wrapper.sh \
    /usr/local/libexec/workcell/apt-helper.sh \
    /usr/local/libexec/workcell/home-control-plane.sh \
    /usr/local/libexec/workcell/public-node-guard.mjs \
    /usr/local/libexec/workcell/provider-policy.sh \
    /usr/local/libexec/workcell/runtime-user.sh \
    /opt/workcell/adapters/claude/hooks/guard-bash.sh \
  && find /opt/workcell/adapters /etc/claude-code /usr/local/libexec/workcell -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}" \
  && touch -h -d "@${SOURCE_DATE_EPOCH}" \
    /etc/ld.so.preload \
    /usr/local/lib/libworkcell_exec_guard.so \
  && rm -rf /tmp/* /var/tmp/* /root/.npm /root/.cache

FROM runtime-base AS runtime

ARG SOURCE_DATE_EPOCH

COPY --from=provider-builder /opt/workcell/providers /opt/workcell/providers
COPY --from=runtime-builder /etc/claude-code/managed-settings.json /etc/claude-code/managed-settings.json
COPY --from=runtime-builder /etc/ld.so.preload /etc/ld.so.preload
COPY --from=runtime-builder /opt/workcell/adapters /opt/workcell/adapters
COPY --from=runtime-builder /usr/local/lib/libworkcell_exec_guard.so /usr/local/lib/libworkcell_exec_guard.so
COPY --from=runtime-builder /usr/local/libexec/workcell /usr/local/libexec/workcell

RUN mv /usr/bin/git /usr/local/libexec/workcell/real/git \
  && chmod 0644 /usr/local/libexec/workcell/real/git \
  && mv /usr/local/bin/node /usr/local/libexec/workcell/real/node \
  && chmod 0755 /usr/local/libexec/workcell/real/node \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/local/libexec/workcell/git \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/codex \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/claude \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/libexec/workcell/core/gemini \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/workcell-entrypoint \
  && ln -sf /usr/local/libexec/workcell/apt-wrapper.sh /usr/local/bin/apt \
  && ln -sf /usr/local/libexec/workcell/apt-wrapper.sh /usr/local/bin/apt-get \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/node \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/codex \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/claude \
  && ln -sf /usr/local/libexec/workcell/core/launcher /usr/local/bin/gemini \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/local/bin/git \
  && ln -sf /usr/local/libexec/workcell/core/git /usr/bin/git \
  && chmod 0444 /etc/claude-code/managed-settings.json \
  && chmod 0644 \
    /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js \
    /opt/workcell/providers/node_modules/@google/gemini-cli/dist/index.js \
  && rm -f /usr/bin/perlbug /usr/bin/perldoc /usr/bin/perlthanks \
  && chmod +x \
    /usr/local/libexec/workcell/core/git \
    /usr/local/libexec/workcell/core/launcher \
    /usr/local/libexec/workcell/home-control-plane.sh \
    /usr/local/libexec/workcell/public-node-guard.mjs \
    /usr/local/libexec/workcell/provider-policy.sh \
    /usr/local/bin/workcell-entrypoint \
    /opt/workcell/adapters/claude/hooks/guard-bash.sh \
  && rm -rf /tmp/* /var/tmp/* /root/.npm /root/.cache /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache \
  && rm -f /etc/ld.so.cache \
  && find \
      /opt/workcell \
      /etc/claude-code \
      /etc/ld.so.preload \
      /usr/local/bin \
      /usr/local/lib/libworkcell_exec_guard.so \
      /usr/local/libexec/workcell \
      /usr/bin/git \
      -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"

WORKDIR /workspace

ENV HOME=/state/agent-home
ENV CODEX_HOME=/state/agent-home/.codex
ENV LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
ENV PATH=/usr/local/bin:/usr/bin:/bin

ENTRYPOINT ["/usr/local/bin/workcell-entrypoint"]
