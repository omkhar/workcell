#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

broker_client="/usr/local/libexec/workcell/workcell-apt-broker-client"

if [[ "$(id -u)" == "0" ]]; then
  args=("$@")
  while ((${#args[@]} > 0)); do
    case "${args[0]}" in
      -n)
        args=("${args[@]:1}")
        ;;
      --preserve-env)
        args=("${args[@]:2}")
        ;;
      --preserve-env=*)
        args=("${args[@]:1}")
        ;;
      --)
        args=("${args[@]:1}")
        break
        ;;
      -*)
        break
        ;;
      *)
        break
        ;;
    esac
  done
  if ((${#args[@]} == 0)); then
    echo "Workcell blocked sudo without an explicit command." >&2
    exit 2
  fi
  exec "${args[@]}"
fi

# Everything an unprivileged caller may ask sudo for is a package operation, and
# the broker client is the only thing that performs one. The client parses the
# sudo options itself, admits the package helper and nothing else, and reaches
# the broker over its socket, so the whole invocation goes across as it arrived
# rather than being taken apart by a second copy of sudo's argument grammar
# written in shell.
exec "${broker_client}" --sudo-compat "$@"
