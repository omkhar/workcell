# Policy Core

`policy/` is the generic contract layer for `Workcell`.

It does not try to define one universal provider config format. Instead it
captures the shared rules that every adapter must preserve:

- the runtime boundary is primary
- host secrets and control sockets stay out
- network modes are explicit
- `breakglass` is named and narrow
- lower-assurance GUI modes are clearly labeled

Provider-native config belongs under `adapters/`.
