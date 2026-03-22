FROM node:22-trixie-slim

ARG CODEX_VERSION=0.117.0-alpha.8
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.81
ARG GEMINI_CLI_VERSION=0.34.0

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN apt-get update && apt-get install -y --no-install-recommends \
  bash \
  build-essential \
  ca-certificates \
  curl \
  fd-find \
  git \
  jq \
  less \
  openssh-client \
  procps \
  python3 \
  ripgrep \
  unzip \
  xz-utils \
  && rm -rf /var/lib/apt/lists/*

RUN ln -sf /usr/bin/fdfind /usr/local/bin/fd

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
  && mv "/tmp/codex-${CODEX_ARCH}" /usr/local/bin/codex \
  && chmod +x /usr/local/bin/codex \
  && rm -f /tmp/codex.tar.gz

RUN npm install -g \
  "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  "@google/gemini-cli@${GEMINI_CLI_VERSION}" \
  && npm cache clean --force

RUN mkdir -p /usr/local/libexec/workcell \
  && mv /usr/bin/git /usr/local/libexec/workcell/git \
  && chmod +x /usr/local/libexec/workcell/git

COPY adapters /opt/workcell/adapters
COPY runtime/container/bin/git /usr/local/bin/git
COPY runtime/container/src/workcell_exec_guard.c /tmp/workcell_exec_guard.c
COPY runtime/container/entrypoint.sh /usr/local/bin/workcell-entrypoint

RUN ln -sf /usr/local/bin/git /usr/bin/git \
  && gcc -shared -fPIC -O2 -Wall -Wextra -o /usr/local/lib/libworkcell_exec_guard.so /tmp/workcell_exec_guard.c -ldl \
  && rm -f /tmp/workcell_exec_guard.c \
  && chmod +x /usr/local/bin/git /usr/local/bin/workcell-entrypoint

USER node
WORKDIR /workspace

ENV HOME=/state/agent-home
ENV CODEX_HOME=/state/agent-home/.codex
ENV LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
ENV PATH=/usr/local/bin:/usr/bin:/bin

ENTRYPOINT ["/usr/local/bin/workcell-entrypoint"]
