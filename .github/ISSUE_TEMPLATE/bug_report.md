---
name: Bug report
about: Report a defect in Workcell
title: ''
labels: bug
assignees: ''
---

## Describe the Defect

Describe the defect. For a planned provider or roadmap item, open a feature
request. Refer to `docs/scenario-gaps.md`.

## Reproduction Steps

Give the steps that reproduce the defect:

1. Run `...`
2. Observe `...`
3. Note the failure

## Expected Result

Describe the expected result.

## Environment

- OS: [for example, macOS 26]
- Workcell version: [release or commit SHA]
- Provider: [Codex / Claude / Copilot / Gemini]
- Mode: [strict / development / build / breakglass]
- Docker version: `docker --version`
- Colima version (macOS only): `colima version`

## Relevant logs

Paste the applicable error output. Remove tokens, keys, and personal paths.

## Security note

If the defect can expose a secret or bypass a security control, do not open a
public issue. Use the [private process for vulnerability reports](../../SECURITY.md).
