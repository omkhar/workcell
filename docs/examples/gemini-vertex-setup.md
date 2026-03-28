# Gemini Vertex AI Setup with Workcell

This guide covers configuring Google Vertex AI as the backend for Gemini CLI
inside Workcell.

## When to use Vertex vs API key

| Scenario | Recommended auth |
|---|---|
| Individual developer, personal quota | `GEMINI_API_KEY` via `gemini_env` |
| Enterprise org, GCP project billing | Vertex AI via `gemini_env` |
| Google Cloud Application Default Credentials already provisioned | `gcloud_adc` (supplemental, paired with Vertex env settings) |
| Interactive personal use, no credential file | OAuth interactive login |

Use Vertex when your organization bills Gemini usage through a GCP project,
needs access to region-specific model endpoints, or requires Vertex-specific
governance controls.

## 1. Create the gemini_env file for Vertex

Create a plain text env file with owner-only permissions:

```bash
mkdir -p ~/.config/workcell
install -m 0600 /dev/null ~/.config/workcell/gemini-vertex.env
```

Populate it with Vertex settings:

```
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=your-project-id
GOOGLE_CLOUD_LOCATION=us-central1
```

Replace `your-project-id` with your actual GCP project ID and `us-central1`
with the region where your Vertex endpoint is provisioned. Never substitute a
real API key or service account key inline here unless your Vertex flow
requires `GOOGLE_API_KEY` in addition to the project/location pair.

If your Vertex flow uses `GOOGLE_API_KEY` instead of project/location:

```
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_API_KEY=FIXTURE_FAKE_KEY_DO_NOT_USE
```

> **Note:** `FIXTURE_FAKE_KEY_DO_NOT_USE` is a placeholder. Replace it with
> your actual key. Never commit real keys to files tracked by git.

## 2. Injection policy TOML

Reference the env file in your injection policy:

```toml
version = 1

[credentials]
gemini_env = "/Users/example/.config/workcell/gemini-vertex.env"
```

For supplemental Google ADC (only if your Vertex flow requires it alongside
the `gemini_env` settings):

```toml
version = 1

[credentials]
gemini_env = "/Users/example/.config/workcell/gemini-vertex.env"
gcloud_adc = "/Users/example/.config/gcloud/application_default_credentials.json"
```

`gcloud_adc` mounts the ADC file to
`~/.config/gcloud/application_default_credentials.json` inside the session.
It is a supplemental input only; it does not activate Vertex mode by itself.
The `gemini_env` entry with `GOOGLE_GENAI_USE_VERTEXAI=true` must also be
present.

## 3. Egress allowlist expansion

When `GOOGLE_CLOUD_LOCATION` (or `GOOGLE_CLOUD_REGION`, `CLOUD_ML_REGION`,
`VERTEX_LOCATION`, or `VERTEX_AI_LOCATION`) is present in the env file,
Workcell derives the corresponding regional allowlist entry at launch time.

For `GOOGLE_CLOUD_LOCATION=us-central1`, Workcell adds:

```
us-central1-aiplatform.googleapis.com:443
```

For `GOOGLE_CLOUD_LOCATION=europe-west4`:

```
europe-west4-aiplatform.googleapis.com:443
```

This allowlist expansion applies to `strict` and `build` mode sessions. The
derived hostname is added alongside the standard Gemini API endpoints. You do
not need to configure it manually.

## 4. Expected endpoints

On a Vertex session with `us-central1`, the effective allowlisted egress set
includes (among other entries):

```
aiplatform.googleapis.com:443
us-central1-aiplatform.googleapis.com:443
```

These endpoints are where Gemini CLI sends model requests when
`GOOGLE_GENAI_USE_VERTEXAI=true` is active.

## 5. Launch with Vertex credentials

```bash
workcell --prepare --agent gemini --workspace /path/to/repo \
  --injection-policy ~/.config/workcell/injection-policy.toml
```

After the image is prepared:

```bash
workcell --agent gemini --workspace /path/to/repo
```

Workcell reads the injection policy from the default location
(`~/.config/workcell/injection-policy.toml`) automatically if it exists.

Confirm the auth mode before launch:

```bash
workcell auth-status --agent gemini --workspace /path/to/repo
```

This prints the primary provider auth mode and the ordered auth mode set
resolved from the injected `gemini_env` content.

## Further reading

- `docs/examples/quickstart-gemini.md` — Gemini quickstart
- `docs/injection-policy.md` — full injection policy reference
- `adapters/gemini/README.md` — Gemini adapter and auth mode details
