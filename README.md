# PingMon
A sysadmin/NOC-grade ICMP ping monitoring tool with long-term analytics: a Go API backend and a web frontend.

## Overview

PingMon continuously pings many hosts, persists every result to SQLite, and serves a REST API for live status and long-term analysis. The server is API-only; the frontend is a separate web app that consumes the API.

### Features

- **Multi-host monitoring** with **per-host** ping config (interval, timeout, packet size), display name, tags, and notes
- **Long-term persistence** in SQLite with **hourly/daily rollups** for fast historical queries over months of data
- **Analysis**: aggregated time-series (raw→minute→hour→day), percentiles (p50/p95/p99), jitter, uptime, and outage detection
- **Organization**: host **groups** and **comments/incidents/maintenance** annotations pinned to the timeline
- **Operations**: live NOC status overview, per-host **alert thresholds**
- **Access**: optional **Bearer-token auth**, fully-open CORS, and **multi-backend** support (one UI, many servers)
- **Read-only / demo mode** (`--readonly`): freeze all writes for a view-only or public-demo deployment
- **ARP discovery** (`--arpscan`): periodically scan neighbors on all interfaces (ip/mac/name, accumulated) at `GET /api/arp`
- **Config file** (`/etc/pingmon.conf`): all settings in one `KEY=VALUE` file; validate with `pingmon config test`, install the default with `pingmon config set`
- **CLI commands**: `pingmon token` (generate a token), `pingmon arp` (scan), `pingmon stats` (stored-state summary), `pingmon config test|set`, store-backed `pingmon host`/`pingmon group` management, and `pingmon systemctl install|uninstall|enable|disable|status` (systemd service) — plus an extensible command framework

See the [Server Documentation](./server/README.md) for the full API, flags, and behavior.

## Project Structure

The application is divided into two main components:

- **Server**: Go backend that handles ICMP ping operations
- **Client**: React/Vite frontend for visualization and user interaction

## Requirements

- **Go 1.21+** (the module declares `go 1.25`; the toolchain is fetched automatically)
- **Node.js 18+** (for the client)
- **Root/Administrator privileges** (required for ICMP operations)

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/7c/pingmon.git
   cd pingmon
   ```

2. Install server dependencies:
   ```bash
   cd server
   go mod tidy
   ```

3. Install client dependencies:
   ```bash
   cd ../client
   npm install
   ```

## Architecture note

The server and client are **independent**. The server is an API-only Go service
(it does not serve the frontend), and the client is a separate web application
that talks to the server's REST API. Deploy and run them separately.

## Building

Build the server binary (output: `bin/pingmon`, git-ignored):

```bash
make build
```

Other targets: `make run` (build + run as root), `make test`, `make race`,
`make vet`, `make fmt`, `make tidy`, `make clean`, `make help`.

The client is built and served on its own:

```bash
cd client
npm run build   # or: npm run dev
```

## Running

The server requires root/administrator privileges to use ICMP ping:

```bash
sudo ./bin/pingmon          # or: make run
```

On start it prints a colorized overview and listens on `127.0.0.1:6868` by
default; the API lives under `/api` (health check: `GET /api/ping`). Data is
persisted under `./data` (`--datafolder`). Common flags: `--host`, `--port`,
`--datafolder`, `--token` (optional auth), `--name`, `--debug`. See
`./bin/pingmon --help` or the [Server Documentation](./server/README.md) for the
full list and the API contract.

The client points at the server's API via its own configuration (the dev server
proxies `/api` to the server port).

## Documentation

For more detailed information:

- [Server Documentation](./server/README.md)
- [Client Documentation](./client/README.md)

## License

[MIT License](LICENSE)
