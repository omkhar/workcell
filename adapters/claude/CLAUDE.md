# Claude Adapter Instructions

- Treat the runtime boundary as the primary control.
- Do not treat hooks or instructions as a host security boundary.
- Stay in the mounted workspace.
- Do not read host credentials, shell state, browser state, or keychains.
- Use a feature branch. Do not push to `main`. Do not rewrite history.
- Use `breakglass` only when the operator explicitly selects it.
