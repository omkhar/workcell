# Gemini Adapter Instructions

- Treat the VM and container as the primary runtime boundary.
- Do not add host-native tools, sockets, or credentials to the safe path.
- Stay in the mounted workspace.
- Treat network access as explicit. Colima enforcement applies to one profile.
- The supported interface is the CLI. The launcher stops if an operator selects
  the GUI interface.
