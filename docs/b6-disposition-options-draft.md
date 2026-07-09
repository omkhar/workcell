# B6 Real-Boundary Certification Lane — Disposition Options (DRAFT)

**Status: DRAFT for maintainer decision. This document does NOT decide B6.** B6
(a real-boundary certification lane on Apple Silicon runner infrastructure) is a
human/funding decision, not an autonomous one. This draft lays out the options,
their tradeoffs against the merged evidence, and a recommendation. The maintainer
records the decision.

Context: B6 is defined in
[improvement-tracks-implementation-plan.md → B6](improvement-tracks-implementation-plan.md)
and is 1.0 release-criteria item 6 in
[ROADMAP.md](../ROADMAP.md#10-release-criteria). Its stated exit gate is a
scheduled lane that launches the strict Colima path and runs the certification
smoke on a real Apple Silicon boundary, with the runner treated as lower-trust
per the CI threat model (no repo secrets beyond scoped runner registration).
Note a scope gap the maintainer should resolve: B6's *tracked step wording* names
only the strict Colima path, but ROADMAP criterion 3 requires BOTH supported
Apple Silicon launch boundaries certified on the release matrix — strict Colima
AND docker-desktop compat (`policy/host-support-matrix.tsv`). So the
real-boundary evidence surface B6 must ultimately cover is both boundaries, not
Colima alone; whichever option is chosen must account for both.

Prepared 2026-07-08 against `main`. Verified: no self-hosted Apple Silicon
runner lane exists on `main` today. Do NOT conflate two different backends when
weighing the B6 go/no-go:

- **There are TWO supported, launch-allowed 1.0 operator boundaries on Apple
  Silicon macOS** (`policy/host-support-matrix.tsv`; both `supported`/`allowed`),
  and ROADMAP criterion 3 requires BOTH certified on the release matrix:
  `macos/arm64/local_vm/colima/strict` (the strict Colima VM+container path) and
  `macos/arm64/local_compat/docker-desktop/compat` (the lower-assurance Docker
  Desktop compat path). The real-boundary evidence for both is the
  **local-operator certification** lane — a live containerized launch on real
  Apple Silicon hardware (the scenario suite under `tests/scenarios/`, driven by
  `scripts/run-scenario-tests.sh`, run on real hardware rather than a hosted
  runner), recorded per `docs/install-lifecycle.md` (§"Local-operator /
  published-release remainder"). **The B6 lane scope therefore covers BOTH
  boundaries**, not Colima alone; it is NOT the C1 evaluation.
- **C1 was the Apple `container` BACKEND evaluation** — a *different* backend
  that is preview/launch-blocked and NOT one of the two supported launch
  boundaries above (`docs/apple-container-evaluation.md`). Its macOS 26.5.1
  capture is evidence about the apple-container preview, not about either
  supported boundary, and must not be read as strict-Colima or compat
  certification.

CI today does NOT run either live boundary: the hosted runtime exercise is the
`container-smoke` job on `ubuntu-latest` running `./scripts/container-smoke.sh`
under **Docker** (a hosted Docker container-smoke lane), and the
`trusted-linux-amd64-validator` lane (`policy/host-support-matrix.tsv`) is
`validation-host-only`/`blocked` — neither is a live macOS VM-plus-container
boundary. Both the strict-Colima and docker-desktop-compat operator boundaries
are exercised ONLY by local-operator certification, never in hosted CI.

## What "real-boundary" means for the supported tier

The supported, launch-allowed operator boundaries are BOTH
`macos/arm64/local_vm/colima/strict` and
`macos/arm64/local_compat/docker-desktop/compat` (both `supported`/`allowed` in
`policy/host-support-matrix.tsv`; ROADMAP criterion 3 requires both certified).
Hosted CI cannot exercise either today: GitHub-hosted macOS runners do not offer
the nested virtualization the Colima VM (or a Docker Desktop VM) needs, and the
Linux `amd64` validators run a *different* host architecture. What hosted CI does
run is a Docker container-smoke lane on `ubuntu-latest`, not either macOS
boundary. So the gap B6 closes is: **a scheduled, real Apple-Silicon-macOS launch
in CI across both supported boundaries** (strict Colima and docker-desktop
compat), versus the current local-operator certification of those paths.

## Option A — Fund/provision a self-hosted Apple Silicon runner

Stand up self-hosted Apple Silicon macOS runner infrastructure and implement the
scheduled certification lane B6 describes.

**What 1.0 gains:**

- A recurring, real launch across both supported boundaries (strict Colima and
  docker-desktop compat) in CI, catching regressions the local-operator cert
  would only catch out-of-band.
- Fully satisfies release-criteria item 6's B6 clause as written.
- Continuous evidence for the Apple `container` promotion path (C1 → B6), which
  is currently deferred post-1.0 precisely because this lane does not exist.

**Costs and security tradeoffs:**

- **Funding + ops:** dedicated Apple Silicon hardware (or a paid hosted
  Apple-Silicon runner service), plus lifecycle ownership (patching, runner
  registration rotation, availability).
- **Trust tier:** per the CI/CD threat model (B9), a self-hosted runner is
  **lower-trust** than the hosted control plane. It must carry **no repo secrets
  beyond scoped, rotatable runner registration**, run untrusted PR code only
  under the documented isolation, and never gain signing authority (host-side
  signing stays off the runner). This must be documented in the threat model as
  part of the exit gate.
- **Blast radius:** a compromised self-hosted runner is a foothold; the mitigation
  is ephemerality (fresh VM per job), least privilege, and no long-lived
  credentials — all of which add ops cost.

## Option B — Amend criterion 6 to defer the hosted real-boundary lane post-1.0

Option B is NOT an auto-satisfied checkbox. Release-criteria item 6 REQUIRES a
real-boundary certification lane (B6) *in place*; the roadmap does not list B6
among the gaps that "explicit disposition" alone can satisfy (that
explicit-disposition allowance is scoped to the SLSA L3 gaps, separately). So
choosing Option B means the maintainer must **amend release-criteria item 6** —
an explicit, recorded criterion change / scope reduction — to defer the hosted
real-boundary Apple Silicon lane to post-1.0, accepting the reduced assurance
stated below. This is a real decision the maintainer owns, not a box the draft
can tick.

Under the amended criterion, 1.0 would ship with the existing **hosted Docker
smoke lane** plus **local-operator certification** of both supported Apple
Silicon boundaries (strict Colima and docker-desktop compat), and B6 (the hosted
real-boundary Apple Silicon lane) would move to post-1.0.

**What 1.0 keeps:**

- Both reviewed boundaries (strict Colima and docker-desktop compat) are still
  certified — via local-operator certification on real Apple Silicon macOS.
- Hosted CI still exercises the containment logic and all deterministic
  invariants: the container-smoke lane runs the runtime image under **Docker on
  a GitHub-hosted `ubuntu-latest` runner**, and the Linux `amd64` validators run
  verify-invariants, the Go engine, and the scenario corpus.
- No new lower-trust runner attack surface is introduced before 1.0.

**What 1.0 loses / accepts (the assurance tradeoff the amendment accepts):**

- **Neither live macOS boundary (strict Colima or docker-desktop compat) is
  exercised in hosted CI at all.** The repo does use GitHub-hosted runners of
  both kinds, but neither exercises a live boundary: the hosted **Linux** runners
  (`ubuntu-latest`) run the Docker `container-smoke` lane, and the hosted
  **macOS** runners (`macos-26`, `macos-15`, the `install-verification` matrix in
  `.github/workflows/ci.yml`) cover install/uninstall mechanics only. Hosted
  macOS runners cannot run the Colima or Docker Desktop VM either — GitHub-hosted
  macOS runners do not expose the nested virtualization those VMs need — so they
  prove install plumbing, not the VM-plus-container boundary. Net: hosted Linux =
  Docker container-smoke; hosted macOS = install-verification only; both live
  macOS boundaries = local-operator certification only. Regressions on either
  supported path are caught by local-operator certification cadence, not by every
  scheduled CI run.
- Release-criteria item 6 is met only by a **recorded amendment** that removes
  the in-place B6 requirement for 1.0 — not by an implemented lane, and not by
  an auto-permitted deferral. The amendment and its assurance tradeoff MUST be
  recorded as a scope decision in the G4 review.
- Apple `container` promotion stays blocked post-1.0 (it already is, per C1).

## Recommendation (maintainer decides)

**Recommended: Option B for 1.0 — amend criterion 6 to defer the hosted
real-boundary lane, with B6 (Option A) scheduled as the first post-1.0 assurance
item** — *if and only if* the maintainer is willing to make that recorded
criterion amendment and judges local-operator certification of both supported
Apple Silicon boundaries (strict Colima and docker-desktop compat) sufficient
boundary evidence for the 1.0 claim.

Rationale grounded in the evidence:

1. 1.0 is a contract-stability and assurance claim, not a platform-reach claim
   (ROADMAP "Path To 1.0"). Both supported boundaries are certified through the
   local-operator discipline — but that certification is a pending 1.0 checklist
   item (it must be run and confirmed on the release matrix for both boundaries,
   not assumed already complete); B6 improves the *cadence and automation* of
   that signal, not the boundaries' correctness.
2. Option A introduces a lower-trust runner attack surface. Standing it up
   *before* 1.0 adds security-ops burden and threat-model work to the exact
   window where the contract surface is being frozen — the opposite of what the
   freeze wants.
3. Criterion 6 as written requires B6 in place, so Option B is a genuine
   scope-reduction decision, not a free deferral. Recommending it means
   recommending that the maintainer amend the criterion with the assurance
   tradeoff recorded — it is honest precisely because it is presented as a
   criterion change the maintainer owns, not a box the draft ticks.
4. B6 is genuinely valuable and should be the first post-1.0 assurance lane
   (it also unblocks the deferred Apple `container` promotion), so Option A is
   "scheduled," not "dropped."

**Pick Option A instead if** the maintainer judges that a 1.0 security-boundary
product must show a *continuous hosted* real-boundary lane (not a periodic
local-operator cert) to meet buyer expectations, and funding/ops for a
lower-trust Apple Silicon runner is available before the freeze.

Either way, the decision and its evidence basis must be recorded in the G4
readiness review (see `docs/1.0-readiness-review-draft.md` §6).
