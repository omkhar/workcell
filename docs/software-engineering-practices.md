# Software Engineering Practices

Use this document as Workcell's durable engineering baseline and improvement
plan. It maps primary sources to repository evidence and bounded work.
It does not change product support, assurance, certification, compliance, or
release claims.

- Assessment date: 2026-08-11.
- Repository commit: `e38f3036ff11a52aee2b04b10b854c51e4d6baf4`.
- GitHub history window: 2026-07-11 10:19 UTC through 2026-08-11 10:19 UTC.
- Next planned review: 2026-11-06.

Engineering health means the measurable condition of development, review,
validation, release, and maintenance work. A risk indicator identifies where
review can help. It does not prove poor quality.

## Evidence Rules

- Prefer primary standards, official product documents, and official project
  guidance.
- Separate observed facts, reasoned inferences, proposals, and external
  constraints.
- Bind measurements to a commit, time window, method, exclusions, and sample
  size.
- Use language-aware parsers for structural metrics.
- Omit a metric when its method is unreliable.
- Use metrics to find constraints and verify improvements.
- Never rank contributors, agents, or models.
- Avoid universal line-count, complexity, coverage, velocity, or duration
  targets.
- Promote advisory measurements into gates only after reviewed evidence proves
  value.

## Source Baseline

The baseline uses primary standards and owner-controlled guidance. A source's
type limits how Workcell applies it.

| Area | Source | Type | Durable Workcell use |
|---|---|---|---|
| Secure development | [NIST SSDF 1.1](https://csrc.nist.gov/pubs/sp/800/218/final) | Government framework | Integrate preparation, protection, production, and vulnerability response across the lifecycle. |
| Trustworthy systems | [NIST SP 800-160 Rev. 1](https://csrc.nist.gov/pubs/sp/800/160/v1/r1/final) | Government engineering guidance | Trace trust objectives through requirements, design, implementation, verification, validation, and operations. |
| Product quality | [ISO/IEC 25010:2023](https://www.iso.org/standard/78176.html) | International standard | Use relevant quality characteristics for requirements, tests, acceptance criteria, and measures. |
| Secure defaults | [CISA Secure by Design](https://www.cisa.gov/sites/default/files/2023-10/Shifting-the-Balance-of-Cybersecurity-Risk-Principles-and-Approaches-for-Secure-by-Design-Software.pdf) | Government guidance | Keep the safe path default and place security responsibility in the product. |
| Supply chain | [SLSA v1.2](https://slsa.dev/spec/v1.2/) and [artifact verification](https://slsa.dev/spec/v1.2/verifying-artifacts) | Approved industry specification | Assess Build and Source tracks separately. Verify artifacts against explicit identities and expectations. |
| Workflow security | [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use) | Platform-owner guidance | Use minimum permissions, immutable action identities, and privilege separation for untrusted content. |
| Code review | [Google Engineering Practices](https://google.github.io/eng-practices/review/reviewer/standard.html) | Organization guidance | Merge small changes that clearly improve code health. Prefer technical evidence over preference. |
| Delivery | [DORA metrics](https://dora.dev/guides/dora-metrics/) | Research-based guidance | Use application-level trends to improve throughput and stability. Do not turn metrics into targets. |
| Operations | [Google SRE monitoring](https://sre.google/sre-book/monitoring-distributed-systems/) | Organization guidance | Prefer simple signals that expose user-visible symptoms and support diagnosis. |
| Go | [Go documentation](https://go.dev/doc/comment) and [table-driven tests](https://go.dev/wiki/TableDrivenTests) | Language-project guidance | Use official conventions, clear APIs, diagnostic failures, and maintainable tests. |
| Rust | [Rust API Guidelines](https://rust-lang.github.io/api-guidelines/) and [Cargo CI guidance](https://doc.rust-lang.org/cargo/guide/continuous-integration.html) | Language-project guidance | Use idiomatic APIs and balance deterministic coverage with CI cost. |
| Open source | [OpenSSF Best Practices Badge](https://openssf.org/projects/best-practices-badge/) | Voluntary program | Use the checklist and Scorecard as evidence. Do not treat either as boundary proof. |

Track selected moving source versions in the
[Standards Watchlist](standards-watchlist.md). A source update does not change a
Workcell claim until a reviewed repository change maps the effect.

## Assessment Method And Evidence

The [versioned aggregate record](engineering-evidence/software-engineering-practices-2026-08-11.json)
contains the methods, exclusions, sample sizes, definitions, and collected
counts. It contains no raw comment body or contributor identity.

Repository facts came from tracked files at the recorded commit. The audit used
exact searches, policy reads, validators, and line counts. It excluded Git
metadata, caches, and untracked content.

Hosted facts came from paginated GitHub APIs. The audit used separate timestamp
queries for created and merged pull requests. It split Actions history into 31
daily windows.

The aggregate record does not preserve raw GitHub responses. Later repository
or hosted changes can make its values historical. H2 and H3 define the durable
collection work.

## Practice Baseline

### Core Design Heuristics

These heuristics guide design choices. They do not override repository
contracts or security invariants.

- Do not repeat yourself (DRY). Keep one authoritative definition for each
  stable rule.
- Do not combine coincidental duplication. Extract shared behavior only after
  evidence proves one stable rule.
- Keep it simple (KISS). Choose the simplest design that preserves contracts,
  invariants, diagnostics, and operations.
- YAGNI means that a demonstrated need justifies each new feature. Add no
  abstraction, option, or workflow without a demonstrated need.
- SOLID groups five object-oriented design principles. Apply these principles
  only where they fit the language and problem.
- **Single-responsibility principle:** Give each module one cohesive
  responsibility.
- **Open-closed principle:** Extend stable behavior for demonstrated variation
  without breaking existing contracts.
- **Liskov substitution principle:** Make each implementation preserve its
  interface contract and observable behavior.
- **Interface segregation principle:** Give each caller the smallest interface
  that it needs.
- **Dependency inversion principle:** Make high-level policy depend on stable
  interfaces, not provider or transport details.
- Keep related behavior and data together.
- Minimize coupling across runtime, policy, adapter, and workflow boundaries.
- Make dependencies, ownership, and side effects explicit.
- Prefer clear code over clever code.
- Delete dead code. Do not keep speculative compatibility paths.
- Preserve compatibility unless a reviewed migration changes the contract.

### Change Design And Review

- Preserve the runtime boundary before optimizing convenience or speed.
- Solve the demonstrated problem with the smallest coherent change.
- Keep unrelated formatting, refactoring, behavior, and support changes in
  separate review units.
- Review design, functionality, complexity, concurrency, tests, documentation,
  and system context.
- Accept a change when it clearly improves code health and all required gates
  pass.
- Record a concrete blocker when required evidence is unavailable.

### Verification

- Select tests from changed risks and contracts.
- Require tests to fail under the defect they claim to detect.
- Use negative, property, mutation, race, integration, and live evidence where
  each method adds value.
- Use coverage as a map of exercised code, not as proof of test quality.
- Keep deterministic regression checks on pull requests.
- Run expensive discovery checks on scheduled, approved, main, or release
  lanes.
- Preserve live certification for supported workflow and boundary changes.

### Security And Supply Chain

- Keep secure defaults, least privilege, explicit exceptions, and fail-closed
  behavior.
- Keep security controls outside provider prompts and configuration.
- Pin dependencies and workflow actions through reviewed repository policy.
- Verify published artifacts against expected source, builder, subject, and
  parameters.
- Scope every SLSA, provenance, certification, and compliance statement to its
  exact evidence.
- Treat signed commits and automated review as controls, not independent human
  approval.

### Delivery And Operations

- Keep the pull-request critical path deterministic and diagnostic.
- Measure distributions and outcomes before setting thresholds.
- Separate queue time, execution time, repair time, and review wait.
- Show sample size, exclusions, incomplete data, and collection failures.
- Use repository trends to improve the system.
- Define a deployment before reporting DORA measures.
- Prefer actionable issue records over noisy alerts.

### Maintainability

- Use size, churn, boundary criticality, defect history, and review burden
  together.
- Extract one behavior-preserving seam in each refactoring review unit.
- Add characterization or differential evidence before moving boundary logic.
- Keep shell as detection, scrubbing, dispatch, and launch glue.
- Put new policy, state, and orchestration logic in Go by default.
- Keep Rust changes within the syscall-interception boundary.
- Remove duplicate validation inventories only after tests prove profile parity.

## Current Assessment

| Area | Observed facts | Reasoned assessment |
|---|---|---|
| Architecture | [System Design](workcell-system-design.md) documents the host launcher, dedicated VM boundary, hardened container, thin adapters, and host-owned records. | Strong and explicit. |
| Contract traceability | The [operator contract](../policy/operator-contract.toml), [requirements](../policy/requirements.toml), documents, scenarios, and evidence have repository validators. | Strong and unusually complete. |
| Verification | At assessment, non-vendored sources had 147 Go test files, 30 Rust test attributes, and 22 scenario scripts. [Validation](validation-scenarios.md) includes fuzz, mutation, smoke, and certification paths. | Broad, but assurance measurement is uneven. |
| Supply chain | [Workflows](github-workflows.md) use full action SHAs and minimum permissions. They run dependency, CodeQL, Scorecard, reproducibility, signing, SBOM, and attestation controls. | Strong, with documented residual risks. |
| Delivery feedback | [Weekly reports](ci-efficiency-and-reliability.md) show workflow totals, averages, failures, reruns, and tracked flaky issues. | Useful foundation, but distributions and durable artifacts are missing. |
| Maintainability | The aggregate record contains exact line counts for three boundary-related shell files. | Concentrated risk signals, not automatic refactoring orders. |
| Governance | The aggregate record identifies active signed-commit, pull-request, thread-resolution, and status-check rulesets. | Strong hosted controls. Single-maintainer operation limits independent review. |
| Release evidence | [Provenance](provenance.md) records signatures, checksums, SBOMs, attestations, reproducibility, and the output-verification gap. | Strong production controls with a known verification gap. |

Release coverage enforces 90 percent for the Rust launcher and 56 percent for
`internal/metadatautil`. It sets no floor for other Go packages. The mutation
policy requires all 14 reviewed mutants to be killed.

The repository has no Go benchmark function or race-detector lane. These
absences are planning signals. They do not prove a user-visible performance or
concurrency defect.

## Delivery History Snapshot

The audit used cursor-paginated pull-request data. It split Actions collection
into 31 paginated daily windows to avoid GitHub's 1,000-result limit.

Merge-time statistics use the 102 pull requests merged in the window.
Percentiles use linear interpolation.

- GitHub recorded 106 created pull requests and 102 merges.
- Median creation-to-merge time was 0.91 hours.
- The p90 creation-to-merge time was 49.46 hours.
- GitHub recorded 490 Codex triggers, 470 submitted reviews, and 305 review
  threads.
- GitHub recorded 1,433 pull-request workflow runs.
- Those runs included 790 successes, 539 skips, 13 failures, and 91
  cancellations.
- Eight workflow runs used a later attempt.
- The p90 workflow-wait proxy was 51.13 hours.

The workflow-wait proxy measures PR creation through the last completed
branch-matched workflow before merge. It is not an individual workflow duration
or a proven critical path.

GitHub history does not identify production deployments or service-level
objectives. Therefore, Workcell cannot report the five current DORA measures
from this data. Release workflow results remain release evidence, not production
service evidence.

## Improvement Plan

### H1: Durable Baseline

Status: implemented by this document and its repository pointers.

Completion evidence:

- The source baseline classifies each primary or owner-controlled source.
- The versioned aggregate record preserves the dated assessment evidence.
- The current assessment separates facts, inferences, plans, and constraints.
- `AGENTS.md` points broad engineering work to this document.
- The Roadmap records the ordered work.

### H2: Deterministic Engineering-Health Evidence

Dependency: H1.

Build a tested generator for one dated, versioned repository baseline.

Acceptance evidence:

- Record the source commit, schema, method, exclusions, and collection time.
- Record file size, 30-day and 90-day churn, tests, requirements, and validation
  lanes.
- Exclude vendor, generated, cache, binary, and temporary content explicitly.
- Use syntax-aware metrics for Go and Rust.
- Keep shell measurements at file level until a reliable parser exists.
- Test the generator against small fixture repositories.

Keep the first baseline advisory. Do not refresh a committed snapshot on every
pull request.

### H3: CI And Review Feedback

Dependencies: H1 and H2.

Extend the weekly CI insight lane with durable JSON and Markdown artifacts.

Acceptance evidence:

- Report count, p50, p95, queue time, execution time, conclusion, and rerun
  ratio.
- Group data by stable workflow identity.
- Add a schema version to each JSON artifact.
- Upload the bounded reports as GitHub Actions artifacts.
- Record the reviewed retention period in `policy/retention-policy.json`.
- Keep `docs/retention-policy.md` consistent with that policy.
- Exclude comment bodies, identities, credentials, and branch names.
- Report pagination gaps, API failures, and missing timestamps.
- Encode absent or partial data instead of reporting a complete result.
- Test calculations with offline JSON fixtures.
- Add exact PR wait states for ready, review request, required green, and
  merge.
- Measure duplicate review triggers for unchanged heads.

Keep these reports read-only and non-gating until a reviewed baseline exists.
Treat unchanged-head and Codex-trigger counts as diagnostic data only. Never
relax the required current-head Codex review loop.

Do not rank people, agents, or models.

### H4: SLSA v1.2 And Release Verification

Dependency: H1.

Reassess provenance against the SLSA v1.2 Build and Source tracks.

Acceptance evidence:

- Use the existing eight-subject SLSA v1.0 Build L2 claim as the comparison
  baseline.
- Do not claim a v1.2 level before this reassessment proves it.
- Record Source-track controls and the single-maintainer constraint.
- Keep Build L3 and Source L4 unclaimed.
- Define trusted builder identities and verification expectations.
- Add read-only post-publication verification for exact-tag release outputs.
- Verify digests, Cosign identity, provenance subject, builder, and expected
  parameters.

Do not rewrite a published tag or release after a verification failure. Follow
the documented next-patch recovery.

### H5: Risk-Based Assurance Expansion

Dependencies: H2 and a reviewed risk or evidence gap.

Add evidence where critical decisions have weak or missing proof.

Acceptance evidence:

- Measure a targeted race-detector lane for concurrency-heavy Go packages.
- Start the lane as a scheduled or approved trial on named high-risk packages.
- Record its duration, resource cost, findings, and false signals.
- Add negative, property, or mutation cases for high-impact decision branches.
- Add extended ACL rejection to the release asset staging path.
  Before you sign the change, publish an authorized immutable fixture release to
  prove it.
- Report mutation scope explicitly.
- Produce coverage maps for selected critical Go package groups.
- Set a ratchet only after reviewed baseline evidence supports it.
- Keep expensive discovery work outside normal pull-request latency.

Promote the race lane only after reviewed evidence proves useful PR feedback.
Otherwise, retain scheduled or on-demand execution.

Do not set one repository-wide coverage percentage. Do not describe the
14-mutant suite as repository-wide mutation testing.

### H6: Measured Complexity Reduction

Dependency: H2.

Select one hotspot with size, churn, boundary, defect, and review evidence.

Acceptance evidence:

- Extract one behavior-preserving seam.
- Preserve output, exit codes, side effects, and first-failure order.
- Add characterization or differential tests before extraction.
- Keep the public contract unchanged.
- Run live certification when a supported workflow can change.

Stop when a refactor requires a behavior or support change. Create a separate
feature unit for that change.

### H7: Validation And Benchmark Maintenance

Dependency: H2.

- Define shared validation sets once while preserving quick and full profiles.
- Prove that each quick profile remains a documented subset of full validation.
- Add Go benchmarks only for paths with an objective or regression history.
- Add shell unit tests only for stable glue or protocol helpers.

Do not build a new framework around logic scheduled for removal.

### H8: Rust Vendor Refresh

Dependency: H1.

Add a locked Rust vendor-refresh workflow. Verify `Cargo.lock`, vendored
sources, offline builds, and reproducibility. Keep dependency updates in
separate review units.

This work does not depend on the engineering-health generator.

### H9: External Controls

Complete these items only when authority and resources exist:

- Add a second qualified maintainer for real independent review.
- Fund trusted Apple Silicon boundary automation.
- Complete a third-party boundary review.
- Assess the OpenSSF badge honestly after known internal gaps close.

The badge is voluntary self-certification. It does not prove Workcell runtime
isolation.

## Execution And Review

Use the repo-local pull-request lifecycle for model routing and publication.
Use representative tasks to compare acceptance success, rework, elapsed time,
tokens, and cost. Never reduce evidence or correctness to lower model cost.

Permit parallel research, review, and local validation on separate scopes.
Serialize GitHub mutations through one coordinator. Merge one review unit before
publishing the next dependent unit.

Review this document each quarter. Update it sooner after a material standard,
architecture, assurance, or delivery-process change.
