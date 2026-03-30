# Gemini Adapter

The Gemini adapter seeds Gemini CLI's session-local home from reviewed
baselines and explicit injection inputs.

Managed surfaces:

- `~/.gemini/settings.json`
- rendered `~/.gemini/GEMINI.md`
- `~/.gemini/.env`
- `~/.gemini/oauth_creds.json`
- `~/.gemini/projects.json`
- `~/.gemini/trustedFolders.json`

Key points:

- Workcell's external VM-plus-container boundary is primary; Gemini's own
  sandbox is optional and not the Tier 1 boundary here
- repo-local `.gemini/` content stays masked on the safe path
- `gemini_env`, `gemini_oauth`, `gemini_projects`, and `gcloud_adc` cover the
  supported long-lived auth and project-state paths
- `breakglass` restores Gemini's own folder-trust prompt behavior
