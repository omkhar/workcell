# Quickstart: Google Gemini in Workcell

This guide walks through running Google Gemini CLI inside the Workcell bounded
runtime. All commands are copy-pasteable.

## Prerequisites

- macOS (the host launcher is macOS only)
- [Colima](https://github.com/abiosoft/colima) installed (`brew install colima`)
- Docker CLI installed (`brew install docker`)
- Workcell installed (`./scripts/install.sh` from the repo root)

Verify the install:

```bash
workcell --version
```

## 1. Credential setup

Workcell does not forward host environment variables or the host home directory
into the session. Credentials reach the agent only through an explicit
injection policy. Three auth modes are supported.

### Option A: API key via gemini_env

The Gemini CLI reads credentials from `~/.gemini/.env` inside the session.
Workcell populates this file from the `credentials.gemini_env` injection
policy key.

Create the env file with owner-only permissions:

```bash
mkdir -p ~/.config/workcell
install -m 0600 /dev/null ~/.config/workcell/gemini.env
# Write the following line (replace with your actual key, not the placeholder)
echo 'GEMINI_API_KEY=FIXTURE_FAKE_KEY_DO_NOT_USE' > ~/.config/workcell/gemini.env
chmod 0600 ~/.config/workcell/gemini.env
```

Create `~/.config/workcell/injection-policy.toml`:

```toml
version = 1

[credentials]
gemini_env = "/Users/example/.config/workcell/gemini.env"
```

> **Note:** `FIXTURE_FAKE_KEY_DO_NOT_USE` is a placeholder. Replace it with
> your actual key. Never commit real keys to files tracked by git.

### Option B: Persisted OAuth credential

If you have an existing Gemini OAuth credential file on the host:

```toml
version = 1

[credentials]
gemini_oauth = "/Users/example/.config/workcell/gemini-oauth.json"
```

Workcell copies this file to `~/.gemini/oauth_creds.json` inside the session.

### Option C: Interactive OAuth login (no credential file needed)

> **Note (lower-assurance):** Interactive OAuth requires network access to
> Google login endpoints. On `strict`, this is within the allowlisted egress
> set for Google auth. No credential file is required on the host, but the
> login flow opens a browser on the host. This gives less reproducibility than
> a pre-provisioned credential file.

Launch without a `gemini_env` or `gemini_oauth` entry; Gemini CLI will prompt
for login interactively on first use.

## 2. Prepare the runtime image

The `strict` profile requires a prebuilt runtime image. Run `--prepare` on
first launch or after a Workcell update:

```bash
workcell --prepare --agent gemini --workspace /path/to/repo
```

## 3. Basic launch

```bash
workcell --agent gemini --workspace /path/to/repo
```

Workcell starts the dedicated Colima VM profile and launches Gemini CLI inside
the hardened inner container. The workspace mounts at `/workspace`.

The default autonomy mode is `yolo` (`--approval-mode yolo`). Gemini proceeds
without per-action approval prompts.

## 4. trustedFolders.json is auto-seeded

On the safe path (`strict` and `build`), Workcell seeds
`~/.gemini/trustedFolders.json` with `/workspace` as a trusted folder before
each provider launch. You will not see Gemini's restart-based folder-trust
prompt inside managed sessions because the trust registry is already correct
at startup.

In `breakglass` mode, the `trustedFolders.json` seeding is omitted so Gemini's
own folder-trust prompt behavior is preserved.

## 5. Vertex AI setup

For Vertex AI, see the dedicated guide at
[`docs/examples/gemini-vertex-setup.md`](./gemini-vertex-setup.md).

A minimal Vertex `gemini.env` file looks like:

```
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=your-project-id
GOOGLE_CLOUD_LOCATION=us-central1
```

When `GOOGLE_CLOUD_LOCATION` is present in the env file, Workcell
automatically derives the regional `us-central1-aiplatform.googleapis.com:443`
entry and adds it to the egress allowlist for strict-mode sessions.

## 6. Publish a PR after work is done

Final branch publication is a host-side action.

Prepare the PR metadata:

```bash
echo "Add feature X" > /tmp/pr-title.txt
cat > /tmp/pr-body.md <<'EOF'
## Summary

- Implements feature X

## Test plan

- [ ] Tests pass
EOF
```

Then publish from the host:

```bash
workcell publish-pr \
  --branch feature-branch \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --workspace /path/to/repo
```

`publish-pr` creates or switches to the named branch, makes a signed commit
using the operator's host identity, pushes it, and opens a draft PR.

## Further reading

- `docs/examples/gemini-vertex-setup.md` — Vertex AI setup guide
- `docs/injection-policy.md` — full injection policy reference
- `docs/invariants.md` — the seven security invariants
- `adapters/gemini/README.md` — Gemini adapter overview
- `docs/adapter-control-planes.md` — full control file matrix
