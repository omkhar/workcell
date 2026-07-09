# Workcell 1.0 Gate Execution — RESUME PROMPT (2026-07-09)

Feed this whole file back to Claude, then run:
`/loop babysit exection actively to load balance, fix stalls etc, progress every 5 mins, prose and status bar till done`

Repo: `/Users/omkharanarasaratnam/src/workcell` (adjust path on the new machine). `origin` = `github.com/omkhar/workcell`.

---

## MISSION
The Workcell **1.0 implementation roadmap is COMPLETE** — every v0.13–v0.15 feature is merged to `main`; A5 (signed session audit records) was the last, merged as a 5-PR stack after a ~20-round Codex review. We are now in the **v1.0-rc GATE phase**: recording gate decisions, finishing residual items, and preparing the release. `main` is at `98905c84` (or later).

## EXECUTION MODEL (CRITICAL — changed 2026-07-09)
**Claude quotas expired.** New tiering, in force until the user says otherwise:
- **fable (this main loop): ORCHESTRATION ONLY** — plan, dispatch, adjudicate Codex reviews, drive PRs to merge, make judgment calls. Do NOT do implementation edits yourself except tiny review-loop doc-consistency fixes on a PR you're already driving.
- **codex-cli executes** the actual implementation: dispatch a **haiku** `Agent` (isolation:"worktree") whose job is to run `codex exec --dangerously-bypass-approvals-and-sandbox --color never "<task>"`, then verify/commit/report. Codex-cli 0.143.0 at `/opt/homebrew/bin/codex`. The haiku agent is thin glue; codex does the heavy lifting (keeps Claude cost minimal).
- **Codex GitHub review-bot loop continues unchanged** (the `@codex review` PR loop below).
- Plan and prompts are the SAME — only the executing model changed.
- Watch for cheaper-model misses (e.g. the D6 agent ignored the 1200-line PR-shape cap; fable must catch these).

## STANDING CONSTRAINTS (non-negotiable)
- **GPG-sign every commit**: `git -c user.email=omkhar@gmail.com commit -S …` (key `DA5A8E9F536C42FD`). Unsigned → the ruleset blocks merge.
- **Feature branches only.** Never push/commit to `main`. Admin-merge is AUTHORIZED once checks + Codex LGTM + threads are clean (`gh pr merge --merge --admin`).
- **check-pr-shape <1200 changed lines per PR** (CI-enforced; `--max-files 25 --max-areas 8 --max-binaries 0`). Split large work into stacked sub-1200 PRs.
- Before pushing scenario/shell changes: run the **FULL `go test ./...`** (not just the touched package — a stale mutation-anchor test bit us) AND the **real colima scenario**: `docker context use colima; export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock; bash tests/scenarios/shared/test-session-commands.sh` → expect `SCENARIO_EXIT=0`. The docker-free "functions-copy seam" is necessary-but-insufficient (it hid 3 real scenario bugs). Run CI-strict validators unfiltered: `shellcheck -x`, `shfmt -ln=bash -i 2 -ci`, `codespell --config .codespellrc`, `markdownlint`, `check-doc-links.sh`, `check-dead-code.sh`, `verify-control-plane-manifest.sh` (regen with `scripts/generate-control-plane-manifest.sh runtime/container/control-plane-manifest.json` after any `scripts/workcell` change), `verify-operator-contract.sh`, `check-public-contract.sh`.
- **No apostrophes/backticks inside `bash -lc '…'` single-quoted blocks** (they terminate the string; broke scenario parsing twice).
- React 👍/👎 on EVERY Codex finding (feeds reviewer tuning).

## CODEX REVIEW-BOT LOOP (per PR, to merge)
1. Post a **standalone** `@codex review` comment (never bury the mention in prose). Snapshot `SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)`.
2. Poll all 3 channels for bot `chatgpt-codex-connector[bot]` comments `created_at > SINCE`. Fix findings (fresh signed commit), push, re-trigger. React 👍 real / 👎 false-positive.
3. **LGTM gate**: merge only when the latest `Didn't find any major issues` issue-comment's `Reviewed commit: <sha>` is a **prefix of the PR head SHA**. Then resolve unresolved review threads via GraphQL `resolveReviewThread`, then `gh pr merge --merge --admin`.
4. **Known false positive**: Codex periodically invents an "unsigned commit" finding citing a NONEXISTENT sha. Verify: `gh api repos/omkhar/workcell/commits/<head>` → `.commit.verification.verified == true`. If verified, 👎 + reply with evidence + resolve thread; it's a keyring-less-sandbox artifact.
5. **GitHub CDN 504 flakes**: jobs that download tools (zizmor, actionlint, apt) intermittently fail with `curl: (22) 504`. These are infra, not content — `gh run rerun <id> --failed` (the watcher auto-reruns, bounded to ~6). The watcher-loop bash pattern for all this is in the session history (a `for i in $(seq 1 82)` poll that checks bucket==fail / new findings / LGTM-SHA-match then admin-merges + auto-reruns 504s).

## USER GATE DECISIONS (recorded, MERGED in #481)
- **B6**: NO funding for a self-hosted Apple-Silicon runner (only this MacBook). → criterion 6 amended: defer the automated runner post-1.0; 1.0 requires **local-operator certification of both boundaries on the MacBook**.
- **B7**: dropped/deferred post-1.0 → criterion 2 amended: third-party audit + OpenSSF badge deferred post-1.0.
- **D3, D6, G3, G4, G1**: "do it all" — execute.

## BATCH STATE
- ✅ **Batch 1 — criteria amendments + readiness sync: DONE, MERGED (#481).** ROADMAP criteria 2/6 amended, A5-merged corrected, B6/B7 deferral synced across the implementation plan, track bullets, checklists, detail sections, and b6-disposition doc. Took 8 Codex consistency rounds.
- ⏳ **Batch 2 — D6 split + D3 residual: BRANCHES DONE + VALIDATED + PUSHED. Just need to run the PR/Codex loop.**
  - `d6a/pinnedinputs-split-1` (pushed, base main, tip SHA `9037817b`, `d6a~2==origin/main`, **689 changed <1200**, signed). 2 commits: `12798e15` `^d` D3 scope-narrowing doc (in `docs/improvement-tracks-implementation-plan.md`) **+** `9037817b` `.r` Node/npm extract → `pinnedinputs_node.go`. **D3 doc is INCLUDED — confirmed.**
  - `d6b/pinnedinputs-split-2` (pushed, stacked, tip `e076f87f`, `d6b~1==d6a`, **574 changed <1200**, signed): `.r` Docker extract → `pinnedinputs_docker.go`. Rust (~129) + Python (7-line placeholder) intentionally stay inline (accepted remainder; the D6 doc note records this).
  - Validation already done on both tips: gofmt clean, go build/vet OK, `go test ./... -race` **0 FAIL**, check-dead-code clean, check-pr-shape PASS each, markdownlint clean. Extracted files are `cmp` byte-identical to the tested `gate/d6-split-d3`. `pinnedinputs.go` is now **1041 lines** (was 1509).
  - `gate/d6-split-d3` (pushed, SHA `e3f0e3d9`): the reference full-split; per-ecosystem files are authoritative.
  - **NEXT for Batch 2 (all that's left)**: (a) Open **PR for d6a (base main)**, run the Codex `@codex review` loop to LGTM, merge. (b) Rebase **d6b onto the new main**, retarget base main, open PR, Codex loop, merge. Nothing else — code is done and green.
- ⬜ **Batch 3 — B6 + G3 local certification on the MacBook: NOT STARTED.** MUST run on Apple-Silicon macOS.
  - Certify BOTH launch boundaries via local-operator discipline: `macos/arm64/local_vm/colima/strict` AND `macos/arm64/local_compat/docker-desktop/compat` — run `scripts/run-scenario-tests.sh` (+ the scenario suite) on real hardware; record pass.
  - G3 install-lifecycle remainder: verified install against a REAL published cosign-signed release, live `--gc` end-to-end, live Apple-Silicon launch. See `docs/install-lifecycle.md` §"Local-operator / published-release remainder".
  - RECORD results in `docs/install-lifecycle.md` + check the Platform/Operations items in `docs/1.0-readiness-review-draft.md` G4 checklist. This is docs + local runs; do the runs directly (fable) or via a haiku+codex agent, but the actual live launches happen on the Mac.
- ⬜ **Batch 4 — G4 finalize + G1 freeze (LAST): NOT STARTED.**
  - G4: finalize the maintainer go/no-go checklist in `docs/1.0-readiness-review-draft.md` §6 — every item checked or dispositioned per the recorded decisions. Confirm no unresolved P0/P1; every support-matrix row matches shipped behavior.
  - G1 (part 2, LAST — run only after everything else): contract-surface **freeze** + published **deprecation policy** on top of the merged public-contract inventory (`policy/public-contract.toml`, `docs/stability-contract.md`). Version bump, changelog, tag plan, freeze checklist, drift gate in release preflight.

## CLEANUP / HOUSEKEEPING
- Local worktrees to prune when convenient: `.claude/worktrees/agent-a7ee19594cc9a18e2` (old A5, locked), `.claude/worktrees/agent-afa3aca086a1d35f1` (D6 first attempt), `scratchpad/g4fix5`. `git worktree remove --force <path>`.
- Open non-gate PR: `#438` dependabot (actions/attest bump) — merge or leave; not part of the gate.
- The a5* branches are all merged; remotes auto-deleted.

## MEMORY (already saved under ~/.claude/…/memory/, may be machine-local)
- `feedback_docker-free-seam-misses-scenario-bugs` — run the REAL colima scenario, not just the seam.
- `feedback_codex-lgtm-and-local-tests` — LGTM by reviewed-commit SHA==head; full `go test ./...` before push.
- `feedback_ci-strict-shellcheck-codespell` — CI is stricter than loose local runs.
- `project_1.0-execution-plan` — updated: roadmap implemented, gate hand-off active.

## ONE-LINE STATUS AT HANDOFF
Roadmap fully implemented & merged. Gate Batch 1 merged. Batch 2 (D6) branches d6a+d6b pushed & shape-legal — need D3-doc verify + PRs through Codex loop. Batch 3 (local cert on Mac) + Batch 4 (G4/G1 freeze, LAST) queued. Model tiering: fable orchestrates, codex-cli (via haiku) executes.
