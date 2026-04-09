# Quickstart: Gemini in Workcell

## Prerequisites

- Workcell installed with `./scripts/install.sh`
- a repo you want to mount as the workspace
- either a reviewed Gemini env file or reviewed cached OAuth material

## 1. Create or update the injection policy

API key or Vertex env-file path:

```bash
workcell auth init
workcell auth set \
  --agent gemini \
  --credential gemini_env \
  --source /Users/example/.config/workcell/gemini.env
```

Cached OAuth path:

```bash
workcell auth set \
  --agent gemini \
  --credential gemini_oauth \
  --source /Users/example/.config/workcell/gemini-oauth.json
```

## 2. Optional explicit prepare

A normal strict launch prepares the reviewed runtime image automatically when
needed:

```bash
workcell --agent gemini --workspace /path/to/repo
```

Use `--prepare-only` when you want to prewarm without launching:

```bash
workcell --prepare-only --agent gemini --workspace /path/to/repo
```

## 3. Inspect the derived posture

```bash
workcell --agent gemini --doctor --workspace /path/to/repo
workcell --agent gemini --inspect --workspace /path/to/repo
workcell auth status --agent gemini
workcell --agent gemini --auth-status --workspace /path/to/repo
```

## 4. Launch Gemini

```bash
workcell --agent gemini --workspace /path/to/repo
```

Gemini's trusted-folders registry is seeded on the managed path so `/workspace`
is already trusted inside the ephemeral session home.

Managed development lane:

```bash
workcell --agent gemini --mode development --workspace /path/to/repo -- bash -lc 'npm test'
```

## 5. Vertex supplement

If the env file configures Vertex and you need ADC as a supplemental input:

```bash
workcell auth set \
  --agent gemini \
  --credential gcloud_adc \
  --source /Users/example/.config/gcloud/application_default_credentials.json
```

## 6. Publish the result on the host

```bash
workcell publish-pr --workspace /path/to/repo --branch feature/my-change \
  --title-file /tmp/pr-title.txt --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

## Further reading

- [docs/injection-policy.md](../injection-policy.md)
- [docs/examples/gemini-vertex-setup.md](gemini-vertex-setup.md)
- [docs/adapter-control-planes.md](../adapter-control-planes.md)
