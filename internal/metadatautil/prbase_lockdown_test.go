// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

const prBaseLockdownPath = ".github/workflows/pr-base-policy.yml"

// prBaseLockdownWorkflow mirrors the reviewed pr-base-policy.yml; each case
// below swaps one piece of it for an escape the lockdown must refuse.
const prBaseLockdownWorkflow = `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened
      - edited

permissions: {}

defaults:
  run:
    shell: bash --noprofile --norc -euo pipefail {0}

concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    timeout-minutes: 5
    env:
      WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}
      WORKCELL_PR_IS_DRAFT: ${{ github.event.pull_request.draft }}
    steps:
      - name: Allow supported main-based PRs
        if: env.WORKCELL_PR_BASE_REF == 'main'
        run: |
          echo "main remains the only supported PR base by default."

      - name: Block unsupported non-main ready PRs
        if: ${{ env.WORKCELL_PR_BASE_REF != 'main' && env.WORKCELL_PR_IS_DRAFT != 'true' }}
        run: |
          echo "Ready pull requests must target main." >&2
          exit 1
`

func TestPRBaseLockdownAcceptsReviewedShape(t *testing.T) {
	t.Parallel()
	if err := metadatautil.IsSafePullRequestTargetWorkflow(prBaseLockdownWorkflow, prBaseLockdownPath); err != nil {
		t.Fatalf("IsSafePullRequestTargetWorkflow(reviewed shape) error = %v", err)
	}
}

func TestPRBaseLockdownAcceptsThisRepository(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile(filepath.Join("..", "..", prBaseLockdownPath))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", prBaseLockdownPath, err)
	}
	if err := metadatautil.IsSafePullRequestTargetWorkflow(string(text), prBaseLockdownPath); err != nil {
		t.Fatalf("IsSafePullRequestTargetWorkflow(repository %s) error = %v", prBaseLockdownPath, err)
	}
}

func TestPRBaseLockdownRefusesEscapes(t *testing.T) {
	t.Parallel()
	const reviewedRun = `        run: |
          echo "main remains the only supported PR base by default."
`
	const reviewedEnv = `      WORKCELL_PR_IS_DRAFT: ${{ github.event.pull_request.draft }}
`
	const reviewedTimeout = `    timeout-minutes: 5
`
	cases := []struct {
		name, old, new, want string
	}{
		{"secret in env", reviewedEnv, reviewedEnv + "      TOKEN: ${{ secrets.RELEASE_TOKEN }}\n", `must not read "secrets.RELEASE_TOKEN"`},
		{"github token in env", reviewedEnv, reviewedEnv + "      TOKEN: ${{ github.token }}\n", `must not read "github.token"`},
		{"whole github context", reviewedEnv, reviewedEnv + "      CTX: ${{ toJSON(github) }}\n", `must not index or call "toJSON"`},
		{"bracket index", reviewedEnv, reviewedEnv + "      T: ${{ github['token'] }}\n", `must not index or call "github"`},
		{"pull request title", reviewedEnv, reviewedEnv + "      T: ${{ github.event.pull_request.title }}\n", `must not read "github.event.pull_request.title"`},
		{"secret in step name", "      - name: Allow supported main-based PRs\n", "      - name: Allow ${{ secrets.X }}\n", `must not read "secrets.X"`},
		{"secret in if", "        if: env.WORKCELL_PR_BASE_REF == 'main'\n", "        if: secrets.X != ''\n", `must not read "secrets.X"`},
		{"unterminated expression", reviewedEnv, reviewedEnv + "      T: ${{ github.workflow\n", "unterminated expression"},
		{"command substitution", reviewedRun, "        run: |\n          echo \"$(curl -s https://example.invalid)\"\n", "may only echo fixed text or exit"},
		{"backticks", reviewedRun, "        run: |\n          echo \"`id`\"\n", "may only echo fixed text or exit"},
		{"fetch pull request code", reviewedRun, "        run: |\n          git fetch origin refs/pull/1/head\n", "may only echo fixed text or exit"},
		{"expression in run", reviewedRun, "        run: |\n          echo \"${{ github.workflow }}\"\n", "may only echo fixed text or exit"},
		{"text after echo", reviewedRun, "        run: |\n          echo \"ok\"; curl https://example.invalid\n", "may only echo fixed text or exit"},
		{"step shell", reviewedRun, reviewedRun + "        shell: python {0}\n", `must not set "shell"`},
		{"step env", reviewedRun, reviewedRun + "        env:\n          A: b\n", `must not set "env"`},
		{"continue on error", reviewedRun, reviewedRun + "        continue-on-error: true\n", `must not set "continue-on-error"`},
		{"job environment", reviewedTimeout, reviewedTimeout + "    environment: release\n", `must not set "environment"`},
		{"job container", reviewedTimeout, reviewedTimeout + "    container: ubuntu:24.04\n", `must not set "container"`},
		{"job if", reviewedTimeout, reviewedTimeout + "    if: true\n", `must not set "if"`},
		{"self-hosted runner", "    runs-on: ubuntu-latest\n", "    runs-on: self-hosted\n", "GitHub-hosted ubuntu runner"},
		{"custom default shell", "    shell: bash --noprofile --norc -euo pipefail {0}\n", "    shell: sh -c 'curl x' {0}\n", "defaults.run.shell"},
		{"top-level env", "permissions: {}\n", "permissions: {}\nenv:\n  A: b\n", `must not set "env"`},
		{"second trigger", "      - edited\n", "      - edited\n  push:\n", "pull_request_target only"},
		{"yaml anchor", "      - name: Allow supported main-based PRs\n", "      - &step\n        name: Allow supported main-based PRs\n", "anchors or aliases"},
		{"merge key", reviewedRun, reviewedRun + "        <<: {uses: ./x}\n", `must not set "<<"`},
		{"wrapped if with tail", "        if: env.WORKCELL_PR_BASE_REF == 'main'\n", "        if: ${{ env.WORKCELL_PR_BASE_REF == 'main' }} || secrets.X\n", "one expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workflow := strings.Replace(prBaseLockdownWorkflow, tc.old, tc.new, 1)
			if workflow == prBaseLockdownWorkflow {
				t.Fatalf("fixture lacks %q", tc.old)
			}
			err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, prBaseLockdownPath)
			if err == nil {
				t.Fatalf("IsSafePullRequestTargetWorkflow() accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("IsSafePullRequestTargetWorkflow() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPRBaseLockdownSeesEscapedTriggerSpelling(t *testing.T) {
	t.Parallel()
	for _, workflow := range []string{
		"name: x\non:\n  \"pull_request\\x5ftarget\": {}\npermissions: {}\njobs: {}\n",
		"name: x\non: [push, \"pull_request\\u005ftarget\"]\npermissions: {}\njobs: {}\n",
		"name: x\non: \"pull_request\\x5ftarget\"\npermissions: {}\njobs: {}\n",
	} {
		got, err := metadatautil.WorkflowDeclaresTrigger(workflow, ".github/workflows/other.yml", "pull_request_target")
		if err != nil || !got {
			t.Fatalf("workflowDeclaresTrigger(%q) = %v, %v; want true", workflow, got, err)
		}
	}
}
