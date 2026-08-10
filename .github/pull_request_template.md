# Summary

- change:
- reason:
- user-visible impact:

## Validation

- [ ] I ran the relevant local validation for this change
- [ ] I verified each published commit signature in the base-to-head range
- [ ] Required CI covers the residual validation
- [ ] I updated docs, policy, or verification when the change affects release,
      provenance, hosted controls, or security posture

## Review sweep

- [ ] I checked top-level PR comments
- [ ] I checked inline review comments
- [ ] I checked unresolved review threads
- [ ] I addressed or explicitly dispositioned actionable feedback
- [ ] I posted the standalone `@codex review` request for the current head
- [ ] I checked the trigger reaction and all Codex response channels
- [ ] I reacted to each Codex finding
- [ ] I resolved each addressed Codex review thread
- [ ] Codex reviewed the exact current head and found no actionable issue
- [ ] I will re-check comments after CI turns green before merging
- [ ] I will do one final comment sweep immediately before merging

## Async reviewer note

Check each configured reviewer in `policy/reviewer-identities.toml`. Do not
merge until you address or disposition each actionable comment.

## Security and release impact

- [ ] No release or provenance impact
- [ ] This changes release behavior, release assets, provenance, SBOMs,
      attestations, workflow permissions, or hosted controls
- [ ] I updated the relevant runbook or policy files for that impact

## Merge Gate

- [ ] The PR base is `main`
- [ ] All required checks passed
- [ ] No actionable comment or unresolved thread remains
- [ ] The exact-head Codex marker is clean
- [ ] If I use an admin merge, it bypasses only the missing independent approval
