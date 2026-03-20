# Mattermost Upstage PII Masking Plugin

This plugin connects Mattermost uploaded files to a DocAI Controller PII Inference API. Admins can configure multiple Mattermost bot accounts, each with its own inference endpoint, auth settings, and PII options.

## What It Does

- Receives attached files from Mattermost posts or DMs
- Sends the files to the PII Inference API as `multipart/form-data`
- Returns extracted PII fields back into the channel or thread
- Supports multiple bots with different model aliases and response schemas
- Lets admins override URL, auth mode, auth token, and bot access rules per bot

## Main Capabilities

- Multiple Mattermost bot accounts managed by the plugin
- Per-bot PII options such as `model`, `lang`, `schema`, and `verbose`
- Default masking of sensitive values in bot responses, with per-bot override
- Global service configuration plus per-bot overrides
- Mattermost RHS workflow for choosing a bot and running PII extraction
- Optional vLLM post-processing after PII extraction
- Access control by user, team, and channel

## Project Layout

- `server/`: Go plugin server for Mattermost hooks, bot execution, PII API calls, and admin endpoints
- `webapp/`: React admin console UI and Mattermost RHS integration
- `build/`: Manifest and packaging helpers

## Configuration Model

The plugin stores its current configuration in the `Config` JSON field. A typical structure looks like this:

```json
{
  "service": {
    "base_url": "http://controller-host/inference",
    "auth_mode": "bearer",
    "auth_token": "YOUR_API_KEY",
    "allow_hosts": "controller-host"
  },
  "runtime": {
    "default_timeout_seconds": 30,
    "max_input_length": 4000,
    "max_output_length": 8000,
    "mask_sensitive_data": true,
    "enable_debug_logs": false,
    "enable_usage_logs": true
  },
  "bots": [
    {
      "username": "pii-masker",
      "display_name": "PII Masker",
      "model": "pii",
      "lang": "ko",
      "schema": "oac",
      "verbose": false,
      "mask_sensitive_data": true
    }
  ]
}
```

Legacy Mattermost plugin settings are still read for backward compatibility, but new development should use the structured `Config` payload.

## Development

Server tests:

```bash
go test ./server/...
```

Webapp type check:

```bash
cd webapp
npm run check-types
```

Webapp build:

```bash
cd webapp
npm run build
```

Webapp tests:

```bash
cd webapp
npm run test -- --runInBand
```
