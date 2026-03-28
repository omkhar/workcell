# Gemini Adapter Instructions

- The shared VM plus container runtime is the primary boundary.
- Do not widen trust by enabling host-native tools, sockets, or credentials.
- Stay inside the mounted workspace.
- Treat network access as profile-scoped and explicit.
- Use lower-assurance GUI paths only when the operator has accepted the
  downgrade.
