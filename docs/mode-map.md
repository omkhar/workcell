# Mode Map

Workcell uses two terms throughout the docs:

- `Tier 1`: a provider CLI running fully inside the bounded Workcell runtime
- `strict`: the default managed Tier 1 runtime mode

`--mode` selects one of four lanes:

| `--mode` | Intended use | Key properties |
|---|---|---|
| `strict` | default provider lane | bounded VM plus container, reviewed network posture, repo control-plane masking, provider-focused entrypoint, `--agent-autonomy yolo` by default |
| `development` | managed interactive development lane | same boundary and masking as `strict`, managed non-provider command execution, broader dependency egress, visibly lower assurance than `strict` |
| `build` | image preparation and dependency refresh | broader egress for rebuild and preparation work |
| `breakglass` | explicit higher-trust debugging path | requires `--ack-breakglass=YYYY-MM-DD` using today's UTC date; visibly lower assurance |

`--container-mutability` is orthogonal to `--mode`: `ephemeral` (the
default) allows package-manager mutations and labels the session
`managed-mutable`, while `readonly` blocks package-manager writes and
gives the strongest managed posture available — `--mode strict
--container-mutability readonly` is the lane to pick when no
lower-assurance downgrade is acceptable.

Other defaults that matter:

- `--agent` is always required; there is no default provider
- `--agent-autonomy yolo` is the default; `--agent-autonomy prompt` is the
  explicit lower-assurance opt-out
- `--cache-profile off` is the default
- `--cache-profile standard` keeps a workspace-scoped persistent non-secret
  cache plane for package and compiler caches, but it is an explicit
  lower-assurance path
- strict launches prepare the reviewed runtime image automatically when needed
- interactive launches show a spinner with elapsed time by default; use
  `--no-spinner` to force plain heartbeat updates instead
- `--prepare` and `--prepare-only` remain useful when you want to make that step explicit
