# Slaxi

<p align="center"><img src=".github/ax.svg" width="96" height="96" alt="AX ecosystem"></p>

Slack interface for AX. Each Slack thread maps to one persistent AX session.

## Requirements

- Slaxi and AX in `PATH`
- a Slack app with Socket Mode enabled
- `app_mentions:read`, `im:history`, `chat:write`, and `files:write` bot scopes
- `app_mention` and `message.im` event subscriptions

## Run

Slaxi runs AX as a subprocess:

```sh
SLACK_APP_TOKEN=xapp-... \
SLACK_BOT_TOKEN=xoxb-... \
SLAXI_WORKDIR=/path/to/project \
slaxi
```

Thread mappings are stored under `~/.config/slaxi/sessions` by default. Set `SLAXI_SESSION_DIR` to change it.

## Variables

```text
SLAXI_AX_PATH
SLAXI_BASE_URL
SLAXI_MODEL
SLAXI_SYSTEM_FILE
SLAXI_WORKDIR
SLAXI_SESSION_DIR
```

## Test

```sh
go test ./...
```
