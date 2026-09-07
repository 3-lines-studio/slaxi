# Slaxi

<p align="center"><img src=".github/ax.svg" width="96" height="96" alt="AX ecosystem"></p>

Slack interface for AX. Each Slack thread maps to one persistent AX session.

## Requirements

- Slaxi and AX in `PATH`
- a Slack app with Socket Mode enabled
- `app_mentions:read`, `im:history`, `chat:write`, and `files:write` bot scopes
- `app_mention` and `message.im` event subscriptions

## Run

Slaxi is a Botdir consumer. The host starts it from the bot root, and Slaxi runs AX as a subprocess:

```sh
cd /path/to/bot
slaxi
```

Slaxi reads `bot.toml` for `model`, `base_url`, and the `[slack]` table (`mention_only`). It reads Slack secrets from the environment (`SLACK_APP_TOKEN`, `SLACK_BOT_TOKEN`) or from `secrets/slack-app-token` and `secrets/slack-bot-token`. It fails readiness when a required Slack or model secret is absent.

Session files live under `state/ax/sessions/<team>/<channel>/<thread>.jsonl`; transport state uses `state/slack/`; generated artifacts go to `workspace/slack/artifacts/`; ephemeral data uses `run/slack/`. The mutable paths are created on start.

## Variables

```text
BOT_ROOT           # bot root anchor (default: current directory)
SLAXI_AX_PATH
SLAXI_BASE_URL     # overrides bot.toml base_url
SLAXI_MODEL        # overrides bot.toml model
```

`SLACK_APP_TOKEN`, `SLACK_BOT_TOKEN`, and `OPENAI_API_KEY` are also read directly from the environment before falling back to `secrets/` files.

## Diagnose

Validate a bot root before starting it — it reports exactly which secrets, the `ax` binary, or `bot.toml` tools are missing:

```sh
cd /path/to/bot
slaxi doctor
```

## Test

```sh
go test ./...
```
