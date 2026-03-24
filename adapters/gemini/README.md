# Gemini Adapter

This adapter maps the shared Workcell runtime boundary into Gemini CLI's native
control surface:

- `~/.gemini/settings.json`
- `GEMINI.md`
- optional injected `projects.json`

Gemini CLI exposes its own sandbox setting, but the shared external runtime is
the primary boundary here. Nested sandboxing is optional and not required for
Tier 1.

On the safe path, repo-local `.gemini/` workspace files stay masked. Use
Workcell injection policy inputs such as `documents.gemini` or
`credentials.gemini_projects` when you want reviewed Gemini state or
instructions to persist across launches.

Gemini CLI is Tier 1 when it runs fully inside the bounded runtime. GUI or IDE
integration is lower assurance unless it is only a client to the same bounded
executor.
