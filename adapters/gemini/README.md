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

On the managed strict/build path, the adapter seeds Gemini's session-local
trusted-folders registry for `/workspace` and disables Gemini CLI's own
folder-trust gate in the managed baseline. Workcell already masks repo-local
Gemini control files and seeds the only trusted Gemini home inside the bounded
runtime, so surfacing Gemini's restart-based trust flow there would add friction
without adding a stronger boundary. In `breakglass`, Workcell restores Gemini's
own folder-trust prompt behavior because the workspace control plane is no
longer masked.

When Gemini auth is injected, Workcell also preselects the matching Gemini auth
mode inside the session-local settings when it can do so unambiguously.
`GEMINI_API_KEY` and Google-login flows can stand alone. Vertex flows must set
`GOOGLE_GENAI_USE_VERTEXAI=true` and then provide either `GOOGLE_API_KEY` or
both `GOOGLE_CLOUD_PROJECT` and `GOOGLE_CLOUD_LOCATION`. `gcloud_adc` is only a
supplemental Vertex input and must be paired with explicit Vertex settings in
`credentials.gemini_env`.

Gemini CLI is Tier 1 when it runs fully inside the bounded runtime. GUI or IDE
integration is lower assurance unless it is only a client to the same bounded
executor.
