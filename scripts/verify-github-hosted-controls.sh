#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool gh
require_tool python3

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"

REPO="${1:-}"
if [[ -z "${REPO}" ]]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gh-controls.XXXXXX")"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

gh api "repos/${REPO}" >"${TMP_DIR}/repo.json"
gh api "repos/${REPO}/actions/permissions" >"${TMP_DIR}/actions-permissions.json"
gh api "repos/${REPO}/actions/variables?per_page=100" >"${TMP_DIR}/actions-variables.json"
gh api "repos/${REPO}/collaborators?affiliation=direct&per_page=100" >"${TMP_DIR}/collaborators-direct.json"
gh api "repos/${REPO}/rulesets" >"${TMP_DIR}/rulesets-summary.json"
python3 - "${TMP_DIR}" "${REPO}" <<'PY'
import json
import pathlib
import subprocess
import sys

tmp_dir = pathlib.Path(sys.argv[1])
repo = sys.argv[2]
summary = json.loads((tmp_dir / "rulesets-summary.json").read_text(encoding="utf-8"))
details = []
for ruleset in summary:
    details.append(
        json.loads(
            subprocess.check_output(
                ["gh", "api", f"repos/{repo}/rulesets/{ruleset['id']}"],
                text=True,
            )
        )
    )
(tmp_dir / "rulesets.json").write_text(json.dumps(details), encoding="utf-8")
PY
gh api "repos/${REPO}/environments" >"${TMP_DIR}/environments.json"
if gh api "repos/${REPO}/environments/release" >"${TMP_DIR}/environment-release.json" 2>/dev/null; then
  :
else
  echo "Missing required release environment on ${REPO}" >&2
  exit 1
fi

python3 - "${TMP_DIR}" "${REPO}" "${POLICY_PATH}" <<'PY'
import json
import pathlib
import sys
import tomllib

tmp_dir = pathlib.Path(sys.argv[1])
repo = sys.argv[2]
policy_path = pathlib.Path(sys.argv[3])

repo_meta = json.loads((tmp_dir / "repo.json").read_text(encoding="utf-8"))
actions_permissions = json.loads((tmp_dir / "actions-permissions.json").read_text(encoding="utf-8"))
actions_variables = json.loads((tmp_dir / "actions-variables.json").read_text(encoding="utf-8"))
direct_collaborators = json.loads((tmp_dir / "collaborators-direct.json").read_text(encoding="utf-8"))
rulesets = json.loads((tmp_dir / "rulesets.json").read_text(encoding="utf-8"))
release_env = json.loads((tmp_dir / "environment-release.json").read_text(encoding="utf-8"))
policy = tomllib.loads(policy_path.read_text(encoding="utf-8"))

default_branch = repo_meta["default_branch"]
owner_login = repo_meta["owner"]["login"]
owner_type = repo_meta["owner"]["type"]
branch_integrity_policy = policy.get("branch_integrity")
if not isinstance(branch_integrity_policy, dict):
    raise SystemExit(
        f"{policy_path} must define branch_integrity as a table with explicit booleans"
    )
for key in ("require_signed_commits", "block_force_pushes", "block_deletions"):
    if branch_integrity_policy.get(key) is not True:
        raise SystemExit(
            f"{policy_path} must set branch_integrity.{key} = true"
        )
branch_review_mode = policy.get("branch_review", {}).get("mode", "review-gated")
if branch_review_mode not in {"review-gated", "single-owner-private-pr"}:
    raise SystemExit(
        f"{policy_path} must set branch_review.mode to "
        f"'review-gated' or 'single-owner-private-pr'"
    )
release_mode = policy.get("release_environment", {}).get("mode", "review-gated")
if release_mode not in {"review-gated", "single-owner-private", "plan-limited-private"}:
    raise SystemExit(
        f"{policy_path} must set release_environment.mode to "
        f"'review-gated', 'single-owner-private', or 'plan-limited-private'"
    )
expected_status_contexts = policy.get("required_status_checks", {}).get("contexts")
if not isinstance(expected_status_contexts, list) or not expected_status_contexts:
    raise SystemExit(
        f"{policy_path} must define required_status_checks.contexts as a non-empty array"
    )
if not all(isinstance(context, str) and context for context in expected_status_contexts):
    raise SystemExit(
        f"{policy_path} must define required_status_checks.contexts as non-empty strings"
    )
expected_repo_variables = policy.get("repository_variables", {})
if not isinstance(expected_repo_variables, dict):
    raise SystemExit(
        f"{policy_path} must define repository_variables as a table of exact expected values"
    )
required_attestation_variable = "WORKCELL_ENABLE_GITHUB_ATTESTATIONS"
if not all(
    isinstance(name, str)
    and name
    and isinstance(value, str)
    for name, value in expected_repo_variables.items()
):
    raise SystemExit(
        f"{policy_path} repository_variables entries must map non-empty names to exact string values"
    )
if required_attestation_variable not in expected_repo_variables:
    raise SystemExit(
        f"{policy_path} must declare {required_attestation_variable} in repository_variables"
    )

if not actions_permissions.get("enabled"):
    raise SystemExit(f"GitHub Actions must be enabled on {repo}")
if not actions_permissions.get("sha_pinning_required"):
    raise SystemExit(f"GitHub Actions SHA pinning must be required on {repo}")

active_rulesets = [ruleset for ruleset in rulesets if ruleset.get("enforcement") == "active"]
if not active_rulesets:
    raise SystemExit(f"No active rulesets found on {repo}")

def has_ref_include(ruleset: dict, expected: str) -> bool:
    include = (
        ruleset.get("conditions", {})
        .get("ref_name", {})
        .get("include", [])
    )
    return expected in include

def has_rule(ruleset: dict, rule_type: str) -> dict | None:
    for rule in ruleset.get("rules", []):
        if rule.get("type") == rule_type:
            return rule
    return None

def require_bypass_shape(
    ruleset: dict,
    *,
    actor_type: str,
    bypass_mode: str,
    require_non_empty: bool = False,
) -> None:
    actors = ruleset.get("bypass_actors", [])
    if require_non_empty and not actors:
        raise SystemExit(
            f"Ruleset {ruleset.get('name')} on {repo} must declare an explicit bypass actor"
        )
    for actor in actors:
        if actor.get("actor_type") != actor_type or actor.get("bypass_mode") != bypass_mode:
            raise SystemExit(
                f"Ruleset {ruleset.get('name')} on {repo} must only use "
                f"{actor_type}/{bypass_mode} bypass actors"
            )

branch_integrity = None
branch_review = None
branch_status_checks = None
tag_release = None

for ruleset in active_rulesets:
    if ruleset.get("target") == "branch" and has_ref_include(ruleset, "~DEFAULT_BRANCH"):
        signatures = has_rule(ruleset, "required_signatures")
        non_fast_forward = has_rule(ruleset, "non_fast_forward")
        deletion = has_rule(ruleset, "deletion")
        if signatures and non_fast_forward and deletion:
            branch_integrity = ruleset
        pull_request = has_rule(ruleset, "pull_request")
        if pull_request:
            branch_review = ruleset
        status_checks = has_rule(ruleset, "required_status_checks")
        if status_checks:
            branch_status_checks = ruleset
    if ruleset.get("target") == "tag" and has_ref_include(ruleset, "refs/tags/v*"):
        creation = has_rule(ruleset, "creation")
        update = has_rule(ruleset, "update")
        deletion = has_rule(ruleset, "deletion")
        if creation and update and deletion:
            tag_release = ruleset

if branch_integrity is None:
    raise SystemExit(
        f"Missing active default-branch integrity ruleset on {repo} "
        f"with required_signatures, non_fast_forward, and deletion"
    )

if branch_review is None:
    raise SystemExit(
        f"Missing active default-branch review ruleset on {repo} with a pull_request rule"
    )
if branch_status_checks is None:
    raise SystemExit(
        f"Missing active default-branch status-check ruleset on {repo} "
        f"with a required_status_checks rule"
    )

if branch_integrity.get("bypass_actors"):
    raise SystemExit(
        f"Default-branch integrity ruleset on {repo} must not declare bypass actors"
    )
require_bypass_shape(branch_review, actor_type="RepositoryRole", bypass_mode="pull_request")
if tag_release is None:
    raise SystemExit(
        f"Missing active release-tag ruleset on {repo} for refs/tags/v* with creation/update/deletion protection"
    )
require_bypass_shape(
    tag_release,
    actor_type="RepositoryRole",
    bypass_mode="always",
    require_non_empty=True,
)

pull_request_rule = has_rule(branch_review, "pull_request")
parameters = pull_request_rule.get("parameters", {})
if branch_review_mode == "review-gated":
    if parameters.get("required_approving_review_count", 0) < 1:
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must require at least one approving review"
        )
    if not parameters.get("require_code_owner_review"):
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must require code owner review"
        )
    if not parameters.get("required_review_thread_resolution"):
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must require resolved review threads"
        )
else:
    if not repo_meta.get("private"):
        raise SystemExit(
            f"Branch review mode 'single-owner-private-pr' on {repo} is only valid for private repositories"
        )
    if owner_type != "User":
        raise SystemExit(
            f"Branch review mode 'single-owner-private-pr' on {repo} is only valid for user-owned repositories"
        )
    if len(direct_collaborators) != 1:
        raise SystemExit(
            f"Branch review mode 'single-owner-private-pr' on {repo} requires exactly one direct collaborator"
        )
    if direct_collaborators[0].get("login") != owner_login:
        raise SystemExit(
            f"Branch review mode 'single-owner-private-pr' on {repo} requires the owner to be the only direct collaborator"
        )
    if not direct_collaborators[0].get("permissions", {}).get("admin"):
        raise SystemExit(
            f"Branch review mode 'single-owner-private-pr' on {repo} requires the owner to retain admin permission"
        )
    if parameters.get("required_approving_review_count", 0) != 0:
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must require zero approving reviews in single-owner-private-pr mode"
        )
    if parameters.get("require_code_owner_review"):
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must not require code owner review in single-owner-private-pr mode"
        )
    if parameters.get("required_review_thread_resolution"):
        raise SystemExit(
            f"Default-branch review ruleset on {repo} must not require resolved review threads in single-owner-private-pr mode"
        )

status_rule = has_rule(branch_status_checks, "required_status_checks")
status_parameters = status_rule.get("parameters", {})
if not status_parameters.get("strict_required_status_checks_policy"):
    raise SystemExit(
        f"Default-branch status-check ruleset on {repo} must require strict status checks"
    )
required_status_contexts = {
    entry.get("context")
    for entry in status_parameters.get("required_status_checks", [])
    if entry.get("context")
}
missing_status_contexts = sorted(set(expected_status_contexts) - required_status_contexts)
if missing_status_contexts:
    raise SystemExit(
        f"Default-branch status-check ruleset on {repo} is missing required contexts: "
        + ", ".join(missing_status_contexts)
    )

actual_repo_variables = {
    entry.get("name"): entry.get("value")
    for entry in actions_variables.get("variables", [])
    if entry.get("name")
}
missing_repo_variables = sorted(
    name for name in expected_repo_variables if name not in actual_repo_variables
)
if missing_repo_variables:
    raise SystemExit(
        f"Repository variables missing on {repo}: " + ", ".join(missing_repo_variables)
    )
wrong_repo_variables = sorted(
    name
    for name, expected_value in expected_repo_variables.items()
    if actual_repo_variables.get(name) != expected_value
)
if wrong_repo_variables:
    details = ", ".join(
        f"{name}={actual_repo_variables.get(name)!r} (expected {expected_repo_variables[name]!r})"
        for name in wrong_repo_variables
    )
    raise SystemExit(
        f"Repository variables on {repo} do not match policy: {details}"
    )

protection_rules = release_env.get("protection_rules", [])
reviewer_rules = [rule for rule in protection_rules if rule.get("type") == "required_reviewers"]
admin_bypass_rule = next(
    (rule for rule in protection_rules if rule.get("type") == "admin_bypass"),
    None,
)
if release_mode == "review-gated":
    if not reviewer_rules:
        raise SystemExit(f"Release environment on {repo} must require a human reviewer")
    if not any(rule.get("reviewers") for rule in reviewer_rules):
        raise SystemExit(f"Release environment on {repo} must define at least one reviewer")
    if release_env.get("can_admins_bypass"):
        raise SystemExit(f"Release environment on {repo} must not allow administrator bypass")
    if admin_bypass_rule and admin_bypass_rule.get("enabled"):
        raise SystemExit(f"Release environment on {repo} must not allow administrator bypass")
elif release_mode == "plan-limited-private":
    if not repo_meta.get("private"):
        raise SystemExit(
            f"Release environment mode 'plan-limited-private' on {repo} is only valid for private repositories"
        )
    if reviewer_rules:
        raise SystemExit(
            f"Release environment on {repo} must not define reviewer gates in plan-limited-private mode"
        )
elif not repo_meta.get("private"):
    raise SystemExit(
        f"Release environment mode 'single-owner-private' on {repo} is only valid for private repositories"
    )
elif owner_type != "User":
    raise SystemExit(
        f"Release environment mode 'single-owner-private' on {repo} is only valid for user-owned repositories"
    )
elif len(direct_collaborators) != 1:
    raise SystemExit(
        f"Release environment mode 'single-owner-private' on {repo} requires exactly one direct collaborator"
    )
elif direct_collaborators[0].get("login") != owner_login:
    raise SystemExit(
        f"Release environment mode 'single-owner-private' on {repo} requires the owner to be the only direct collaborator"
    )
elif not direct_collaborators[0].get("permissions", {}).get("admin"):
    raise SystemExit(
        f"Release environment mode 'single-owner-private' on {repo} requires the owner to retain admin permission"
    )

print(
    f"GitHub-hosted controls verified for {repo} "
    f"(default branch: {default_branch}, release mode: {release_mode})."
)
PY
