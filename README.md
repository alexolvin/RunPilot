# RunPilot

**Languages:** English | [Русский](README.ru.md)

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-70b000.svg)](LICENSE)
[![ci](https://github.com/alexolvin/RunPilot/actions/workflows/ci.yml/badge.svg)](https://github.com/alexolvin/RunPilot/actions/workflows/ci.yml)

**RunPilot** is a self-hosted control plane for AI coding agents. It
schedules, proxies, monitors and visualizes coding-agent sessions (Qwen Code
running in tmux) across one or more machines, behind a single static Go binary.

- One static binary (`CGO_ENABLED=0`), three roles: coordinator, node, client
- Slot-based scheduling with leases, queueing and mid-flight migration
- Transparent LLM gateway — coders just point `OPENAI_BASE_URL` at it
- Live web UI: sessions, queue, servers, nodes, journal, monitoring, inline terminal
- Operator modes: `NORMAL` / `PAUSED` / `EMERGENCY` / `SAFE_MODE`
- Built-in monitoring (health, vLLM metrics, GPU) and fault quarantine
- Telegram notifications, retention and backup
- Multi-machine operation over a private overlay network (e.g. Tailscale)

## What it is

RunPilot coordinates coding agents that live in tmux panes. A **coordinator**
makes every decision (scheduling, leasing, monitoring); **nodes** run on the
machines that host the sessions and only manage their local tmux panes; a
single **gateway** sits in front of your LLM servers so the coders talk to one
URL. There is no separate frontend build step — the web UI is embedded into the
binary.

| Role        | Command          | Runs on                                    |
| ----------- | ---------------- | ------------------------------------------ |
| Coordinator | `runpilot serve` | your hub machine                           |
| Node        | `runpilot node`  | each worker machine                        |
| Client      | `runpilot`       | any machine that can reach the coordinator |

```
                 ┌────────────────────────── hub ──────────────────────────┐
                 │                                                         │
 coder ─────────►│  gateway :8787  ──►  LLM server A / B (vLLM)            │
 (in a tmux      │      │                                                  │
  pane)          │      ▼                                                  │
                 │  coordinator :8788  ──  scheduler · monitor · store     │
                 │      ▲  WebSocket                                       │
                 └──────┼──────────────────────────────────────────────────┘
                        │
                 ┌──────┴──────┐
                 │   node      │   manages tmux panes on its own machine
                 └─────────────┘
```

- Coders talk **only to the gateway** (`OPENAI_BASE_URL=http://<coord>:8787/s/<sid>/v1`).
- Nodes talk **only to tmux** on their own machine (`tmux`, `ps`, `nvidia-smi`, `rocm-smi`).
- The **scheduler** is the only component that decides when a session gets a slot.

## Features

- **Session lifecycle** — start, resume, run, attach, peek, close; queue with
  priority, pinning and hold; automatic requeue on failure.
- **Gateway** — transparent reverse proxy for LLM traffic with per-session
  path routing, body-size limits, slot hold and one-shot migration on upstream
  failure before the first byte.
- **Scheduling** — slot leases (not raw GPU load), aging, affinity, cooldowns,
  external-slot detection, recovery window.
- **Monitoring** — periodic health checks, vLLM `/metrics` and GPU sampling,
  fault windows that quarantine a failing server, flapping detection.
- **Operator modes** — `NORMAL`, `PAUSED`, `EMERGENCY` (cancels all in-flight
  generation), `SAFE_MODE` (no new leases, in-flight continue).
- **Web UI** — session list and cards, queue, servers, nodes, journal,
  monitoring, notifications, settings; live pane screen and an inline terminal;
  adopt existing unmanaged tmux panes; light/dark themes; token login.
- **Notifications** — Telegram alerts for faults, OOM, emergency, bind, DB and
  external-process events (coalesced, rate-limited).
- **Data** — SQLite (WAL) storage, automatic retention, daily backup with
  rollback/restore.
- **Adoption** — take over an existing tmux pane (already running an agent) and
  put it under management.

## Requirements

- Go `1.26` to build from source.
- `tmux` on every node (hosts the agent sessions).
- A private network between the hub and the nodes (Tailscale is the reference
  setup). Loopback-only is fine for a single machine.

## Hardware

RunPilot itself is lightweight: the coordinator and nodes do **not** need a
GPU. The GPU-bound part is the model servers you point it at. Each `servers`
entry targets a model server (typically **vLLM**) that runs on a GPU sized for
the model — e.g. a 27B-class model needs a GPU (or pooled GPU) with enough VRAM
to hold the weights plus activation memory. RunPilot reads each server's health,
`/metrics`, and GPU usage, but it never performs inference itself.

## Installation

Build the single binary:

```sh
go build -o runpilot ./cmd/runpilot
```

Then install the services (systemd on Linux; launchd stub on macOS):

```sh
./runpilot service install serve   # coordinator on the hub
./runpilot service install node    # node on each worker
```

Each command generates the unit file, places it, and enables + starts it. The
coordinator reads its config from `~/.config/runpilot/config.yaml` and secrets
from `~/.config/runpilot/env`. A node reads its own `config.yaml` and
`secrets.env` (which carries the node token).

> The binary is fully static (`CGO_ENABLED=0`). Ship the same artifact to every
> machine.

## Quick start

1. Create the coordinator config at `~/.config/runpilot/config.yaml`
   (see [Configuration](#configuration)).
2. Put the operator token in `~/.config/runpilot/env`:
   `RUNPILOT_TOKEN=...`.
3. `./runpilot serve` (or via the installed service).
4. On each worker: `./runpilot node --config ~/.config/runpilot/config.yaml`.
5. Open the web UI on the coordinator (default port `8790`) and log in with the
   token.
6. Start a session — from the web UI, or:

   ```sh
   ./runpilot run --agent qwen --name mytask --dir ~/proj
   ```

The coder inside the session is configured to use the gateway, so its LLM calls
are scheduled, metered and monitored by RunPilot.

## Configuration

Strict YAML (unknown keys are rejected). Full reference and a complete example
live in [`testdata/config.example.yaml`](testdata/config.example.yaml); the
package-level defaults are in
[`internal/config/defaults.go`](internal/config/defaults.go).

Minimal coordinator config:

```yaml
coordinator:
  bind: 127.0.0.1
  allow_cidrs: ["127.0.0.0/8", "100.64.0.0/10"]
  gateway_port: 8787
  api_port: 8788
  token_env: RUNPILOT_TOKEN
  db_path: ~/.local/share/runpilot/runpilot.db

servers:
  - name: srv1
    priority: 100
    slots: 1
    accept: [resume, high, normal, low]
    health_url: http://192.0.2.21:8004/health
    metrics_url: http://192.0.2.21:8004/metrics
    upstreams:
      openai:
        url: http://192.0.2.21:8004
        model: qwen3.6-27b
        key_env: RUNPILOT_KEY_VLLM

profiles:
  qwen: {model_alias: qwen3.6-27b}
```

### Environment variables

| Variable            | Purpose                                                              | Example (non-secret)   | Required   |
| ------------------- | -------------------------------------------------------------------- | ---------------------- | ---------- |
| `RUNPILOT_TOKEN`    | Coordinator/operator credential (API + web login)                    | any long random string | yes        |
| `RUNPILOT_KEY_VLLM` | API key passed to the LLM server (name set per server via `key_env`) | your provider key      | per server |
| `RUNPILOT_TG_TOKEN` | Telegram bot token for alerts                                        | your bot token         | no         |

The node's token lives in the node's `secrets.env` (referenced by the installed
`node` unit through `EnvironmentFile`). Values are read from the environment
only — never stored in the repository.

All thresholds, timeouts and limits are read from configuration — there are no
hard-coded numbers in the hot path.

## Command-line interface

`runpilot` is the client. Common commands:

| Group    | Commands                                                                                 |
| -------- | ---------------------------------------------------------------------------------------- |
| Roles    | `serve`, `node`, `doctor`, `selftest`, `version`                                         |
| Sessions | `run`, `exec`, `ls`, `q`, `peek <t>`, `attach <t>`, `why <t>`                            |
| Queue    | `queue enqueue/dequeue/requeue/prio/pin/prefer/unpin/hold/unhold/set/cancel`             |
| Servers  | `servers`, `server drain/undrain <s>`, `pause`, `resume`                                 |
| Ops      | `events`, `stats`, `backup restore/rollback`, `service install`, `upgrade`, `tmux-setup` |

Run `runpilot <cmd> --help` for flags. Exit codes: `0` success, `1` runtime
error or any `doctor` FAIL, `2` config schema/semantics violation.

## Web UI

Sections: **Sessions**, **Queue**, **Servers**, **Nodes**, **Journal**,
**Monitoring**, **Notifications**, **Settings**. Each session card shows a live
pane screen, status, the active lease, and an **inline terminal** for direct
input. The UI is served by the coordinator and protected by the operator token;
it is meant to be reached over your private network (e.g. `tailscale serve`).

## Architecture

RunPilot is one Go module with a strict package layout:

- `internal/config` — strict schema, the only place defaults live, validation.
- `internal/model` — entities and the state machines as pure functions.
- `internal/store` — SQLite (WAL), migrations, retention.
- `internal/clock` — real/virtual clocks (all time flows through here).
- `internal/gateway` — proxy, slot hold, migration.
- `internal/scheduler` — tick, ranking, admissibility, server choice.
- `internal/monitor` — health, vLLM metrics, GPU, fault quarantine.
- `internal/api` — REST + SSE + node WebSocket.
- `internal/node`, `internal/tmux` — node daemon and tmux calls.
- `internal/detect` — pane classification and input extraction.

Invariants worth knowing: at most N concurrent turns per server (N = its slots);
every state transition is written to SQLite before its side effect; no token
values or request bodies are ever logged. See
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and
[`docs/api.md`](docs/api.md) for detail.

## Project structure

```text
runpilot/
├── cmd/runpilot          # the single binary: coordinator / node / client roles
├── cmd/runpilot-stand    # offline test stand (fixtures, screenshots)
├── internal/             # config, model, store, clock, gateway, scheduler, monitor, api, ...
├── web/                  # embedded web UI (ESM modules, no build step)
├── profiles/             # agent profiles (qwen)
├── testdata/             # fixtures + config.example.yaml
├── tools/                # project linters and generators
├── docs/                 # ARCHITECTURE.md, api.md
├── Makefile
└── go.mod
```

## Development

```sh
go build ./...        # build
go vet ./...          # vet
make lint             # project linters (literal / web / hosts hygiene)
go test -race ./...   # full test suite
```

All four must pass. The linters enforce hygiene rules, including that no real
hostnames or private CGNAT addresses leak into the code.

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — components, invariants, automata
- [`docs/api.md`](docs/api.md) — HTTP/WS/SSE reference
- [`testdata/config.example.yaml`](testdata/config.example.yaml) — full configuration example

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please keep changes focused and the
four checks green.

## Security

See [SECURITY.md](SECURITY.md). Report vulnerabilities privately — do not open a
public issue.

## Code of conduct

This project follows a [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE) © RunPilot contributors.
