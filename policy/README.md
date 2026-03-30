# Policy Core

`policy/` holds the shared contract layer for Workcell.

It exists to define what every adapter and workflow must preserve:

- the runtime boundary is primary
- host secrets and control sockets stay out by default
- network modes are explicit
- `breakglass` is narrow and visibly lower assurance
- hosted controls outside git still require explicit policy

Provider-native config does not live here. That belongs in `adapters/`.
