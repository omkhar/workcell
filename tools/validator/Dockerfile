ARG VALIDATOR_BASE_IMAGE=node:22-trixie-slim@sha256:285a78d9fdb7cd2e4a03639cc7bbf609755ce8c0aa4e3b00086211e3e86a7038
ARG DEBIAN_SNAPSHOT=20260316T000000Z
ARG RUST_VERSION=1.94.1
ARG RUSTUP_VERSION=1.29.0
ARG RUSTUP_INIT_LINUX_X86_64_SHA256=4acc9acc76d5079515b46346a485974457b5a79893cfb01112423c89aeb5aa10
ARG RUSTUP_INIT_LINUX_ARM64_SHA256=9732d6c5e2a098d3521fca8145d826ae0aaa067ef2385ead08e6feac88fa5792

FROM ${VALIDATOR_BASE_IMAGE}

ARG DEBIAN_SNAPSHOT
ARG RUST_VERSION
ARG RUSTUP_VERSION
ARG RUSTUP_INIT_LINUX_X86_64_SHA256
ARG RUSTUP_INIT_LINUX_ARM64_SHA256

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/cargo/bin:${PATH}
ENV RUSTUP_HOME=/usr/local/rustup

RUN rm -f /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie main\n' "${DEBIAN_SNAPSHOT}" > /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie-updates main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/%s trixie-security main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'Acquire::Check-Valid-Until "false";\nAcquire::Retries "5";\nAcquire::http::Timeout "30";\n' > /etc/apt/apt.conf.d/99workcell-validator-snapshot \
  && install_validator_packages() { \
       DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
         ca-certificates \
         codespell \
         curl \
         gcc \
         git \
         golang-go \
         groff-base \
         jq \
         libc6-dev \
         llvm \
         mandoc \
         openssh-client \
         python3 \
         python3-coverage \
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
  && rustup_target="" \
  && rustup_sha256="" \
  && case "$(dpkg --print-architecture)" in \
       amd64) \
         rustup_target="x86_64-unknown-linux-gnu"; \
         rustup_sha256="${RUSTUP_INIT_LINUX_X86_64_SHA256}"; \
         ;; \
       arm64) \
         rustup_target="aarch64-unknown-linux-gnu"; \
         rustup_sha256="${RUSTUP_INIT_LINUX_ARM64_SHA256}"; \
         ;; \
       *) \
         echo "Unsupported dpkg architecture for rustup: $(dpkg --print-architecture)" >&2; \
         exit 1; \
         ;; \
     esac \
  && curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/${rustup_target}/rustup-init" \
    -o /tmp/rustup-init \
  && echo "${rustup_sha256}  /tmp/rustup-init" | sha256sum -c - \
  && chmod 0755 /tmp/rustup-init \
  && /tmp/rustup-init -y --profile minimal --default-toolchain "${RUST_VERSION}" --component rustfmt --component clippy --no-modify-path \
  && ln -sf /usr/local/cargo/bin/cargo /usr/local/bin/cargo \
  && ln -sf /usr/local/cargo/bin/rustc /usr/local/bin/rustc \
  && ln -sf /usr/local/cargo/bin/rustfmt /usr/local/bin/rustfmt \
  && ln -sf /usr/local/cargo/bin/cargo-fmt /usr/local/bin/cargo-fmt \
  && ln -sf /usr/local/cargo/bin/cargo-clippy /usr/local/bin/cargo-clippy \
  && rustc --version | grep -F "rustc ${RUST_VERSION} " >/dev/null \
  && cargo fmt --version >/dev/null \
  && cargo clippy --version >/dev/null \
  && rm -f /tmp/rustup-init \
  && rm -rf \
    /usr/local/rustup/downloads \
    /usr/local/rustup/tmp \
    /var/lib/apt/lists/* \
    /var/cache/apt/* \
    /var/cache/debconf/*-old \
    /var/cache/ldconfig/aux-cache

WORKDIR /workspace

ENV WORKCELL_REQUIRE_SUPPORTED_COVERAGE=1

ENTRYPOINT ["/bin/bash", "-lc"]
