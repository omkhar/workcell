ARG VALIDATOR_BASE_IMAGE=node:22-trixie-slim@sha256:285a78d9fdb7cd2e4a03639cc7bbf609755ce8c0aa4e3b00086211e3e86a7038
ARG DEBIAN_SNAPSHOT=20260316T000000Z

FROM ${VALIDATOR_BASE_IMAGE}

ARG DEBIAN_SNAPSHOT

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN rm -f /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie main\n' "${DEBIAN_SNAPSHOT}" > /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s trixie-updates main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/%s trixie-security main\n' "${DEBIAN_SNAPSHOT}" >> /etc/apt/sources.list \
  && printf 'Acquire::Check-Valid-Until "false";\nAcquire::Retries "5";\nAcquire::http::Timeout "30";\n' > /etc/apt/apt.conf.d/99workcell-remote-validator-snapshot \
  && install_remote_validator_packages() { \
       DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
         codespell \
         cargo \
         docker-cli \
         docker-buildx \
         git \
         groff-base \
         llvm \
         mandoc \
         python3 \
         python3-coverage \
         rustc \
         rustfmt \
         shellcheck \
         shfmt \
         yamllint \
       && return 0; \
     } \
  && for attempt in 1 2 3; do \
       rm -rf /var/lib/apt/lists/*; \
       if DEBIAN_FRONTEND=noninteractive apt-get update \
         && install_remote_validator_packages; then \
         break; \
       fi; \
       if [[ "${attempt}" -eq 3 ]]; then \
         exit 1; \
       fi; \
       sleep "$((attempt * 5))"; \
     done \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/cache/ldconfig/aux-cache

WORKDIR /workspace

ENV WORKCELL_REQUIRE_SUPPORTED_COVERAGE=1

ENTRYPOINT ["/bin/bash", "-lc"]
