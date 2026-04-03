from __future__ import annotations

import unittest

from test_support import repo_root


class ValidationEntrypointTests(unittest.TestCase):
    def test_dev_quick_check_stays_bounded_to_fast_local_work(self) -> None:
        script = (repo_root() / "scripts/dev-quick-check.sh").read_text(encoding="utf-8")

        self.assertIn("python3 -m unittest discover", script)
        self.assertIn("cargo test --locked --offline", script)

        self.assertNotIn("container-smoke.sh", script)
        self.assertNotIn("verify-invariants.sh", script)
        self.assertNotIn("verify-reproducible-build.sh", script)
        self.assertNotIn("verify-release-bundle.sh", script)
        self.assertNotIn("pre-merge.sh", script)
        self.assertNotIn("run-mutation-tests.sh", script)
        self.assertNotIn("verify-coverage.sh", script)

    def test_validation_gates_lint_all_scenario_shell_scripts(self) -> None:
        find_probe = 'find "${ROOT_DIR}/tests/scenarios" -type f -name \'test-*.sh\' -print | sort'

        quick_check = (repo_root() / "scripts/dev-quick-check.sh").read_text(encoding="utf-8")
        validate_repo = (repo_root() / "scripts/validate-repo.sh").read_text(encoding="utf-8")

        self.assertIn(find_probe, quick_check)
        # validate-repo.sh discovers shell files (including scenario tests)
        # via _find_files which calls find with -name patterns
        self.assertIn("_find_files '*.sh'", validate_repo)


if __name__ == "__main__":
    unittest.main()
