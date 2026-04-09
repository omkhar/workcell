#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

usage() {
  local exit_code="${1:-2}"
  cat <<'EOF' >&2
Usage: ./scripts/generate-homebrew-formula.sh VERSION BUNDLE_SHA256 OUTPUT_PATH [--repository owner/repo]
EOF
  exit "${exit_code}"
}

if [[ $# -eq 1 ]] && [[ "${1}" == "-h" || "${1}" == "--help" ]]; then
  usage 0
fi

[[ $# -ge 3 ]] || usage

VERSION="$1"
BUNDLE_SHA256="$2"
OUTPUT_PATH="$3"
shift 3

REPOSITORY="${GITHUB_REPOSITORY:-omkhar/workcell}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repository)
      REPOSITORY="${2:?--repository requires a value}"
      shift 2
      ;;
    -h | --help)
      usage 0
      ;;
    *)
      echo "Unsupported option: $1" >&2
      usage
      ;;
  esac
done

mkdir -p "$(dirname "${OUTPUT_PATH}")"

cat >"${OUTPUT_PATH}" <<EOF
class Workcell < Formula
  desc "Bounded runtime launcher for coding agents"
  homepage "https://github.com/${REPOSITORY}"
  url "https://github.com/${REPOSITORY}/releases/download/${VERSION}/workcell-${VERSION}.tar.gz"
  sha256 "${BUNDLE_SHA256}"
  license "Apache-2.0"

  depends_on "colima"
  depends_on "docker"
  depends_on "gh"
  depends_on "git"
  depends_on "go"

  def install
    odie "Workcell currently supports Apple Silicon macOS hosts only." unless OS.mac?
    odie "Workcell currently supports Apple Silicon macOS hosts only." unless Hardware::CPU.arm?

    libexec.install Dir["*"]
    bin.install_symlink libexec/"scripts/workcell" => "workcell"
    man1.install libexec/"man/workcell.1"
    (share/"doc/workcell").install "README.md", "CONTRIBUTING.md", "SECURITY.md", "SUPPORT.md"
  end

  def caveats
    <<~EOS
      Workcell uses a dedicated Colima VM and explicit host-side credential policy.

      Start here:
        workcell --help
        workcell auth init

      Docs:
        https://github.com/${REPOSITORY}/blob/main/docs/getting-started.md
    EOS
  end

  test do
    assert_match "bounded runtime launcher for coding agents", shell_output("#{bin}/workcell --help")
  end
end
EOF
