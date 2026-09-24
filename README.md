# Lotse

A lightweight, self-hosted monitoring hub for Linux, macOS and Windows machines, in the spirit of
[Beszel](https://github.com/henrygd/beszel), built to later support remote actions such as a shell.

- **Hub**: one Go binary in a Docker container. It includes the web UI, stores data in SQLite, and serves
  the agent installers. Idles at under 10 MB RAM.
- **Agent**: one static Go binary per OS/arch (~8 MB). Runs as a systemd, launchd or Windows service. Idles at
  ~15 MB RAM and ~0 % CPU.

## Architecture

```
Browser ──HTTP + SSE──▶ Hub (Docker) ◀──wss/ws, SSH inside── Agent (Linux / macOS / Windows)
                        ├─ REST API + embedded Svelte UI
                        ├─ SQLite: users, systems, metrics (1m / 10m / 1h rollups)
                        └─ /install.sh, /install.ps1, /download/<agent>
```

- **Agents dial out** to the hub over a WebSocket. Clients need no open ports, and this works behind NAT and
  reverse proxies.
- **SSH inside the WebSocket.** The agent is the SSH server and the hub the SSH client. Each side pins the
  other's Ed25519 key, so the link is authenticated and encrypted even over plain `ws://`. The remote shell
  and file transfer planned for Phase 2 will use standard SSH channels (PTY requests, SFTP).
- **Adaptive reporting.** Agents report every 60 s. While someone has the UI open, the hub switches them to
  every 2 s, and switches back 15 s after the last viewer leaves.
- **Storage.** 1-minute averages are kept for 48 h, 10-minute averages for 31 days and 1-hour averages for
  a year. For about 20 machines the database stays at a few MB.

```
cmd/hub, cmd/agent          entry points
internal/protocol           hub ⇄ agent messages
internal/agent              connection loop, config, collect/ (gopsutil)
internal/hub                HTTP API, auth, agent gateway, live state, event stream
internal/hub/store          SQLite schema, metrics rollups
web/                        Svelte 5 + Vite + Tailwind + uPlot, embedded into the hub
```

## Running the hub

```sh
docker compose up -d --build
```

Open `http://<docker-host>:8090`. On first visit you create the admin account.

| Variable        | Default        | Meaning                                                                   |
| --------------- | -------------- | ------------------------------------------------------------------------- |
| `HUB_URL`       | _(from browser)_ | Address agents use to reach the hub, e.g. `http://hub.lan:8090`          |
| `HUB_ADDR`      | `:8090`        | Listen address                                                            |
| `HUB_DATA_DIR`  | `/data`        | Database and hub key (`hub_ed25519`); **back this up**                    |
| `HUB_AGENT_DIR` | `/app/agents`  | Agent binaries served for installation                                    |

The hub key in `/data/hub_ed25519` is the identity every agent pins. If you lose it, all agents must be
reinstalled.

## Adding machines

Click **Add system**. The dialog shows a one-line command for Linux, macOS and Windows. The command:

1. downloads the right agent binary from the hub,
2. writes the config (hub URL, pinned hub key, enrollment token) with root/SYSTEM-only permissions,
3. installs and starts the service.

An enrollment token is valid for one hour and can enroll any number of machines. An agent deletes its token
once the hub has accepted it.

Tokens can also be created without the UI, e.g. for Ansible:

```sh
docker exec lotse-hub /app/hub token --ttl 2h
```

| OS      | Binary                             | Config and key                                  | Logs                                 |
| ------- | ---------------------------------- | ----------------------------------------------- | ------------------------------------ |
| Linux   | `/usr/local/bin/lotse-agent`  | `/etc/lotse-agent/`                        | `journalctl -u lotse-agent`     |
| macOS   | `/usr/local/bin/lotse-agent`  | `/Library/Application Support/lotse-agent/`| `/Library/Logs/lotse-agent.log` |
| Windows | `C:\Program Files\lotse-agent\` | `C:\ProgramData\lotse-agent\`            | `C:\ProgramData\lotse-agent\agent.log` |

To remove an agent, run `sudo lotse-agent uninstall --purge` (or the same command in an elevated
PowerShell), then delete the binary.

## Security model

- The web UI uses bcrypt passwords, HttpOnly `SameSite=Strict` session cookies, an Origin check on every
  write, a strict CSP, and a 5-minute lockout after 5 failed logins per IP.
- Agents only accept the hub key pinned at install time. The hub only accepts agents whose key it has
  enrolled, or that present a valid, unexpired enrollment token.
- Session and enrollment tokens are stored as SHA-256 hashes.
- Deleting a system in the UI drops its connection. The agent cannot rejoin without a new token.
- The install command fetches the installer over whatever scheme the hub URL uses. On an untrusted network,
  put the hub behind HTTPS (Caddy, Traefik) and set `HUB_URL=https://…`.

## Development

Go 1.27 and Node 24, or just Docker:

```sh
make test          # go test -race ./...
make check         # + go vet + svelte-check
make dev-hub       # hub on :8090 with ./data
make dev-web       # Vite on :5173 with hot reload, proxies /api to the hub
make agents        # cross-compile all agents into dist/agents (served by a local hub)
```

Run an agent in the foreground against a local hub without installing a service:

```sh
lotse-agent run --config ./dev/agent.json
```

## Roadmap

1. ~~MVP: hub, agents, enrollment, CPU/memory/disk/network/load, dashboard, Docker image~~
2. Remote shell (xterm.js ⇄ hub ⇄ SSH channel ⇄ PTY/ConPTY), audit log, TOTP/passkeys, re-authentication
   before opening a shell, agent-side `allow_shell` switch
3. Alerts (thresholds, offline) via ntfy, Telegram, Discord or e-mail; Docker container stats; processes
4. Saved scripts across hosts, file transfer (SFTP), signed agent self-update, reboot and service actions
