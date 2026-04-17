# Gemini Vertex AI Setup with Workcell

Use this pattern when Gemini should authenticate through Vertex instead of a
plain Gemini API key.

## 1. Create the Gemini env file

```dotenv
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=my-project
GOOGLE_CLOUD_LOCATION=us-central1
```

If your flow also needs ADC, keep it as a separate reviewed credential file.

## 2. Create the injection policy

```toml
version = 1

[credentials]
gemini_env = "/Users/example/.config/workcell/gemini-vertex.env"
gcloud_adc = "/Users/example/.config/gcloud/application_default_credentials.json"
```

## 3. What Workcell does with this

- seeds `~/.gemini/.env` inside the session
- treats ADC as a supplemental input rather than a standalone auth mode
- derives the matching regional `aiplatform.googleapis.com` allowlist entry for
  strict-mode launches when a location is present

## 4. Launch

```bash
workcell --prepare --agent gemini --workspace /path/to/repo
```

## Related docs

- [docs/injection-policy.md](../injection-policy.md)
- [docs/examples/quickstart-gemini.md](quickstart-gemini.md)
- [docs/requirements-validation.md](../requirements-validation.md)
- [docs/validation-scenarios.md](../validation-scenarios.md)
