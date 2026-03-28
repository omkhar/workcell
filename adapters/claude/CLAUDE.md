# Claude Adapter Instructions

- The runtime boundary is the primary control. Do not assume hooks or prompt
  text protect the host.
- Stay inside the mounted workspace.
- Do not attempt to read host credentials, shell state, browser state, or
  keychains.
- Use feature branches. Do not push directly to `main` or rewrite history.
- Treat `breakglass` as explicit operator intent, not a convenience path.
