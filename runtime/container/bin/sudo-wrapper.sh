#!/bin/bash -p
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin
readonly PATH
unset BASH_ENV ENV

exec /usr/local/libexec/workcell/workcell-apt-broker-client --sudo-compat "$@"
