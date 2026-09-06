#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"${ROOT_DIR}/scripts/install-dev-tools.sh"
git -C "${ROOT_DIR}" config core.hooksPath .githooks
echo "Configured repo hooks: .githooks"
"${ROOT_DIR}/scripts/dev-quick-check.sh"

cat <<'EOF'
Bootstrap complete.

Suggested next step:
  ./scripts/pre-merge.sh
EOF
