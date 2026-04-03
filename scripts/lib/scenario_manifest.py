#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path, PurePosixPath
from typing import NoReturn


VALID_LANES = {"secretless", "provider-e2e"}
VALID_PLATFORMS = {"any", "linux", "macos"}
VALID_PROVIDERS = {"codex", "claude", "gemini"}
PERSONA_PATTERN = re.compile(r"^[a-z][a-z0-9-]*$")


def die(message: str) -> NoReturn:
    raise SystemExit(message)


def _require_nonempty_string(value: object, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        die(f"{label} must be a non-empty string")
    return value


def _optional_bool(scenario: dict[str, object], key: str, scenario_id: str, default: bool) -> bool:
    if key not in scenario:
        return default
    value = scenario[key]
    if not isinstance(value, bool):
        die(f"scenario {scenario_id}: {key} must be boolean")
    return value


def _normalize_test_file(value: object, scenario_id: str, manual: bool) -> str:
    if value in ("", None):
        if manual:
            return ""
        die(f"scenario {scenario_id}: test_file is required for automated scenarios")

    if not isinstance(value, str):
        die(f"scenario {scenario_id}: test_file must be a string")

    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("", ".", "..") for part in path.parts):
        die(f"scenario {scenario_id}: test_file must stay under tests/scenarios without traversal")
    if path.suffix != ".sh" or not path.name.startswith("test-"):
        die(f"scenario {scenario_id}: test_file must reference a scenario shell script")
    return path.as_posix()


def load_scenarios(manifest_path: Path) -> list[dict[str, object]]:
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        die(f"Scenario manifest does not exist: {exc.filename}")

    if not isinstance(manifest, dict):
        die("scenario manifest must contain a JSON object")
    if manifest.get("version") != 1:
        die("scenario manifest version must be 1")

    raw_scenarios = manifest.get("scenarios")
    if not isinstance(raw_scenarios, list) or not raw_scenarios:
        die("scenario manifest must contain a non-empty scenarios array")

    seen_ids: set[str] = set()
    seen_test_files: set[str] = set()
    scenarios: list[dict[str, object]] = []
    for index, raw in enumerate(raw_scenarios, start=1):
        if not isinstance(raw, dict):
            die(f"scenario entry {index} must be an object")

        scenario_id = _require_nonempty_string(raw.get("id"), f"scenario entry {index} id")
        if scenario_id in seen_ids:
            die(f"Duplicate scenario id: {scenario_id}")
        seen_ids.add(scenario_id)

        description = _require_nonempty_string(
            raw.get("description"), f"scenario {scenario_id} description"
        )
        persona = _require_nonempty_string(raw.get("persona"), f"scenario {scenario_id} persona")
        if not PERSONA_PATTERN.fullmatch(persona):
            die(f"scenario {scenario_id}: persona must match {PERSONA_PATTERN.pattern}")

        providers = raw.get("providers")
        if not isinstance(providers, list) or not providers:
            die(f"scenario {scenario_id}: providers must be a non-empty list")
        normalized_providers: list[str] = []
        for provider in providers:
            provider_name = _require_nonempty_string(provider, f"scenario {scenario_id} provider")
            if provider_name not in VALID_PROVIDERS:
                die(
                    f"scenario {scenario_id}: provider must be one of: {', '.join(sorted(VALID_PROVIDERS))}"
                )
            if provider_name in normalized_providers:
                die(f"scenario {scenario_id}: duplicate provider {provider_name}")
            normalized_providers.append(provider_name)

        requires_credentials = _optional_bool(raw, "requires_credentials", scenario_id, False)
        manual = _optional_bool(raw, "manual", scenario_id, False)

        lane = raw.get("lane", "provider-e2e" if requires_credentials else "secretless")
        if not isinstance(lane, str) or lane not in VALID_LANES:
            die(f"scenario {scenario_id}: lane must be one of: {', '.join(sorted(VALID_LANES))}")
        if lane == "secretless" and requires_credentials:
            die(f"scenario {scenario_id}: cannot be secretless and require credentials")

        platform = raw.get("platform", "any")
        if not isinstance(platform, str) or platform not in VALID_PLATFORMS:
            die(
                f"scenario {scenario_id}: platform must be one of: {', '.join(sorted(VALID_PLATFORMS))}"
            )

        test_file = _normalize_test_file(raw.get("test_file", ""), scenario_id, manual)
        if test_file:
            if test_file in seen_test_files:
                die(f"duplicate scenario test_file: {test_file}")
            seen_test_files.add(test_file)

        scenarios.append(
            {
                "id": scenario_id,
                "description": description,
                "persona": persona,
                "providers": normalized_providers,
                "requires_credentials": requires_credentials,
                "manual": manual,
                "lane": lane,
                "platform": platform,
                "test_file": test_file,
            }
        )

    return scenarios


def scenario_shell_tests(scenario_root: Path) -> set[str]:
    scenario_root = scenario_root.resolve()
    return {
        path.relative_to(scenario_root).as_posix()
        for path in sorted(scenario_root.rglob("test-*.sh"))
        if path.is_file()
    }


def verify_coverage(scenario_root: Path, manifest_path: Path) -> None:
    scenario_root = scenario_root.resolve()
    scenarios = load_scenarios(manifest_path)

    manifest_test_files = {str(scenario["test_file"]) for scenario in scenarios if scenario["test_file"]}
    for test_file in sorted(manifest_test_files):
        full_path = scenario_root / test_file
        if not full_path.is_file():
            die(f"Missing test file: tests/scenarios/{test_file}")

    orphaned_test_files = sorted(scenario_shell_tests(scenario_root) - manifest_test_files)
    if orphaned_test_files:
        joined = ", ".join(orphaned_test_files)
        die(f"Scenario scripts missing from manifest: {joined}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate and enumerate Workcell scenario metadata.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    list_parser = subparsers.add_parser("list-tsv", help="Print validated scenario rows as TSV.")
    list_parser.add_argument("manifest", type=Path)

    verify_parser = subparsers.add_parser(
        "verify-coverage", help="Validate the manifest and ensure shell test coverage parity."
    )
    verify_parser.add_argument("scenario_root", type=Path)
    verify_parser.add_argument("manifest", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.command == "list-tsv":
        for scenario in load_scenarios(args.manifest):
            requires_credentials = "1" if scenario["requires_credentials"] else "0"
            manual = "1" if scenario["manual"] else "0"
            print(
                "\t".join(
                    [
                        str(scenario["id"]),
                        str(scenario["test_file"]),
                        requires_credentials,
                        str(scenario["lane"]),
                        str(scenario["platform"]),
                        manual,
                    ]
                )
            )
        return 0

    if args.command == "verify-coverage":
        verify_coverage(args.scenario_root, args.manifest)
        return 0

    parser.error(f"unsupported command: {args.command}")
    return 2  # unreachable; defensive in case parser.error is overridden


if __name__ == "__main__":
    raise SystemExit(main())
