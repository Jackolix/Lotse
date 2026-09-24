# Lotse

A lightweight, self-hosted monitoring hub for Linux, macOS and Windows machines, in the spirit of
[Beszel](https://github.com/henrygd/beszel), that can also act on them: a browser terminal and Wake-on-LAN.

- **Hub**: one Go binary in a Docker container. It includes the web UI, stores data in SQLite, and serves
  the agent installers. Idles at under 10 MB RAM.
- **Agent**: one static Go binary per OS/arch (~8 MB). Runs as a systemd, launchd or Windows service. Idles at
  ~15 MB RAM and ~0 % CPU.

## Architecture

```
Browser ──HTTP, SSE, WebSocket──▶ Hub (Docker) ◀──wss/ws, SSH inside── Agent (Linux / macOS / Windows)
  xterm.js terminal              ├─ REST API + embedded Svelte UI           PTY / ConPTY shell
                                 ├─ SQLite: users, systems, metrics,        Wake-on-LAN relay
                                 │  audit log
                                 └─ /install.sh, /install.ps1, /download/<agent>
```

- **Agents dial out** to the hub over a WebSocket. Clients need no open ports, and this works behind NAT and
  reverse proxies.
- **SSH inside the WebSocket.** The agent is the SSH server and the hub the SSH client. Each side pins the
  other's Ed25519 key, so the link is authenticated and encrypted even over plain `ws://`. Metrics and
  control messages are SSH global requests; each terminal is a standard SSH session channel with a PTY.
- **Adaptive reporting.** Agents report every 60 s. While someone has the UI open, the hub switches them to
  every 2 s, and switches back 15 s after the last viewer leaves.
- **Storage.** 1-minute averages are kept for 48 h, 10-minute averages for 31 days and 1-hour averages for
  a year. For about 20 machines the database stays at a few MB.

```
cmd/hub, cmd/agent          entry points
internal/protocol           hub ⇄ agent messages
internal/agent              connection loop, config, shell (go-pty), collect/ (gopsutil)
internal/hub                HTTP API, auth + TOTP, agent gateway, terminal bridge, wake, audit, event stream
internal/hub/store          SQLite schema, metrics rollups, audit log
internal/wol                magic packets
web/                        Svelte 5 + Vite + Tailwind + uPlot, embedded into the hub
```

## Running the hub

```sh
docker compose pull && docker compose up -d      # release image from ghcr.io/jackolix/lotse
docker compose up -d --build                     # or build from source
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
docker exec lotse-hub /app/hub token --ttl 2h [--allow-shell]
```

| OS      | Binary                             | Config and key                                  | Logs                                 |
| ------- | ---------------------------------- | ----------------------------------------------- | ------------------------------------ |
| Linux   | `/usr/local/bin/lotse-agent`  | `/etc/lotse-agent/`                        | `journalctl -u lotse-agent`     |
| macOS   | `/usr/local/bin/lotse-agent`  | `/Library/Application Support/lotse-agent/`| `/Library/Logs/lotse-agent.log` |
| Windows | `C:\Program Files\lotse-agent\` | `C:\ProgramData\lotse-agent\`            | `C:\ProgramData\lotse-agent\agent.log` |

Agents are also published on the [releases page](https://github.com/Jackolix/Lotse/releases) with
`sha256sums.txt`, if you'd rather install without the script:

```sh
curl -fLO https://github.com/Jackolix/Lotse/releases/latest/download/lotse-agent-linux-amd64
sudo install lotse-agent-linux-amd64 /usr/local/bin/lotse-agent
sudo lotse-agent install --hub http://hub.lan:8090 --key 'ssh-ed25519 …' --token … [--allow-shell]
```

A hub serves the agents built into its image. If it doesn't carry one, it redirects the download to
its own version on GitHub.

To remove an agent, run `sudo lotse-agent uninstall --purge` (or the same command in an elevated
PowerShell), then delete the binary.

## Remote shell

Open a system and click **Terminal**. You get a full terminal in the browser: bash/zsh on Linux and macOS,
PowerShell on Windows 10 1809 or newer (via ConPTY). The shell runs as the agent's user, which is root or SYSTEM.

- **Opt-in on each machine.** An agent only accepts shells if it was installed with `--allow-shell`
  (Windows: `-AllowShell`, or tick "Allow remote shell" in the Add system dialog). The setting lives in the
  agent's config, which only a local admin can edit. A compromised hub cannot turn it on.
  To change it on an installed machine, re-run the install command with or without the flag.
- **Re-authentication.** Opening a shell asks for your password again, plus your two-factor code if it's on.
  That unlocks shells for 10 minutes, like `sudo`.
- **Audit.** Every shell is logged under **Activity** with user, source IP, duration, bytes and exit status.
  Keystrokes and output are not recorded. Signing out closes your open shells.

## Wake-on-LAN

Offline systems get a **Wake** button. Agents 0.2+ report their network interfaces (MAC address and subnet).
The hub remembers them after a machine goes offline.

Broadcasts don't cross routers or leave a Docker bridge network, so the hub asks an **online agent in the same
subnet** to send the magic packet. If no agent shares the subnet, the hub sends it itself. That only reaches your
LAN when the hub runs with `network_mode: host` (Linux hosts only; see `docker-compose.yml`).

The target must have Wake-on-LAN enabled in its BIOS/UEFI and network driver. It usually only works over wired
Ethernet.

## Security model

- The web UI uses bcrypt passwords, optional TOTP two-factor login (codes can't be reused), HttpOnly
  `SameSite=Strict` session cookies, an Origin check on every write and WebSocket, a strict CSP, and a 5-minute
  lockout after 5 failed logins or re-authentications per IP.
- Opening a shell needs a re-authentication within the last 10 minutes. Changing your password signs out all
  other sessions.
- The activity log records sign-ins (including failed ones), password confirmations, shells, Wake-on-LAN and
  changes to systems. It is kept for a year.
- Agents only accept the hub key pinned at install time. The hub only accepts agents whose key it has
  enrolled, or that present a valid, unexpired enrollment token.
- Session and enrollment tokens are stored as SHA-256 hashes.
- Deleting a system in the UI drops its connection. The agent cannot rejoin without a new token.
- Agents refuse shells unless installed with `--allow-shell`. They send Wake-on-LAN packets only to the broadcast
  addresses of their own networks.
- The install command fetches the installer over whatever scheme the hub URL uses. On an untrusted network,
  put the hub behind HTTPS (Caddy, Traefik) and set `HUB_URL=https://…`.

## Releases

GitHub Actions (`.github/workflows/build.yml`) tests every push and pull request. It runs gofmt, go vet for all
agent platforms, `go test -race` and svelte-check.

- Every push to `main` publishes the Docker image `ghcr.io/jackolix/lotse:main` for amd64 and arm64.
- Pushing a tag like `v0.3.0` publishes `:0.3.0`, `:0.3` and `:latest`, and creates a GitHub release. The release
  contains the agents for every platform, hub binaries for Linux, and checksums.

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
2. ~~Remote shell (xterm.js ⇄ hub ⇄ SSH channel ⇄ PTY/ConPTY), agent-side opt-in, re-authentication, TOTP,
   audit log, Wake-on-LAN via relay agents~~
3. Alerts (thresholds, offline) via ntfy, Telegram, Discord or e-mail; Docker container stats; processes
4. Saved scripts across hosts, file transfer (SFTP), signed agent self-update, reboot and service actions,
   passkeys, more users with roles
