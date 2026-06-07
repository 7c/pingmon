# PingMon Server

The backend server component of the PingMon application.

## Overview

The server is a standalone Go application that handles ICMP ping operations to multiple hosts simultaneously and provides a RESTful API. It manages ping operations, collects and aggregates statistics, and persists everything to SQLite. It is **API-only** — the frontend is a separate application served independently (the server does not bundle or serve any static UI).

## Architecture

The server uses a manager pattern to handle multiple ping operations:

- **PingerManager**: Coordinates all active ping operations
- **Pinger Class**: Encapsulates ping functionality for a single host
- **Store**: SQLite-backed persistence for hosts, results, rollups, groups, and annotations
- **Rollup job**: Background aggregation of raw results into hourly/daily summaries
- **REST API**: The server's only surface; consumed by the separately-deployed frontend or any client

## Persistence & long-term analysis

The server persists state to a SQLite database at `<datafolder>/pingmon.db`
(data folder set via `--datafolder`, default `/var/lib/pingmon`):

- **Monitored hosts** are remembered with **per-host config** (interval, timeout, packet size), display name, tags, notes, and alert thresholds — the server resumes pinging each at its configured interval after a restart.
- **Every ping result** (success/failure, RTT, timestamp, error) is stored long-term. Results are written asynchronously in batches so the ping loop never blocks on disk.
- **Tiered rollups**: a background job aggregates raw results into `ping_rollup_hourly` and `ping_rollup_daily` (min/avg/max/p95, sent/received). Range queries pick raw/hour/day automatically by span, so charts stay fast over months of data.
- **Retention**: raw results older than `--raw-retain` (default 720h; `0` = keep forever) are pruned, but never beyond what has been rolled up — hourly/daily summaries are kept indefinitely.
- **Groups, tags, and annotations** (comments / incidents / maintenance windows) are persisted. Removing a host retains its historical results and annotations.

### Feature summary

- Per-IP ping period/config, updatable live (`PUT /api/hosts/{ip}/config`)
- Aggregated time-series for one host (`GET /api/hosts/{ip}/history?start&end&resolution`) or many (`GET /api/series?ips=…`) for overlay charts
- Derived analytics: availability, percentiles (p50/p95/p99) + jitter, outage detection
- NOC status wall snapshot (`GET /api/status`) with per-host state, sparkline, and breaches
- Host groups with aggregate health; comments/annotations pinned to time points/ranges
- Optional Bearer-token API authentication (see [Authentication](#authentication-optional))

**The complete, authoritative API contract is in [openapi.yml](./openapi.yml)** (v1.3.0).
The endpoint summaries below cover the most common operations.

## Prerequisites

- **Go 1.21+** (the module declares `go 1.25`; Go 1.21+ fetches the required toolchain automatically)
- **Root/Administrator privileges** (required for ICMP operations)

No frontend build is required — the server runs API-only.

## API Endpoints

### GET /api/ping
Public health check — always reachable, **never requires authentication**.
Reports the server's name (the `--name` flag, defaulting to the OS hostname) so
clients can identify the instance.

**Response:**
```json
{ "status": "ok", "name": "ping-eu-1" }
```

### GET /api/profile
Public instance metadata and defaults — **never requires authentication**. Used
by the multi-backend UI when registering this server as a backend.

**Response:**
```json
{
  "name": "ping-eu-1",
  "service": "pingmon",
  "version": "1.7.1",
  "authRequired": true,
  "readOnly": false,
  "arpScan": true,
  "minIntervalMs": 500,
  "serverTime": "2026-06-02T10:00:00Z",
  "defaults": { "intervalMs": 1000, "timeoutMs": 2000, "packetSize": 56 }
}
```

### GET /api/arp
Available only when started with `--arpscan`. Returns neighbors discovered across
all interfaces, accumulated over every scan (`firstSeen`/`lastSeen`/`count`, never
forgotten).

**Response:**
```json
{
  "count": 1,
  "entries": [
    { "ip": "192.168.1.1", "mac": "cc:28:aa:9c:22:00", "names": ["router.lan"],
      "interface": "en0", "firstSeen": "2026-06-02T09:00:00Z",
      "lastSeen": "2026-06-02T10:00:00Z", "count": 60 }
  ]
}
```

### GET /api/pinger
Returns statistics for all running pingers.

**Response:**
```json
{
  "192.168.1.1": {
    "Host": "192.168.1.1",
    "Running": true,
    "Sent": 10,
    "Received": 10,
    "Loss": 0,
    "MinRTT": 1.23,
    "AvgRTT": 2.45,
    "MaxRTT": 3.67,
    "LastUpdate": "2023-06-01T12:34:56Z"
  }
}
```

### Hosts (CRUD)

A RESTful resource for managing monitored IPs. These are the preferred
endpoints; the `/api/pinger/*` routes below are kept for the existing frontend.

#### GET /api/hosts
Lists the IPs currently being monitored (Read).

**Response:**
```json
{
  "ips": ["192.168.1.1", "8.8.8.8"]
}
```

#### POST /api/hosts
Adds one or more IPs to monitor (Create). Accepts a single `ip`, a list of
`ips`, or both. Returns `201` on success, or `206` with a per-IP `errors` map
when some IPs could not be added.

**Request:**
```json
{
  "ips": ["192.168.1.1", "8.8.8.8"]
}
```

#### DELETE /api/hosts/{ip}
Stops monitoring an IP and removes it from the persisted host list (Delete).
Historical results are retained.

#### POST /api/hosts/{ip}/reset
Resets the in-memory statistics for an IP (Update).

#### GET /api/hosts/{ip}/history
Returns persisted ping results for an IP, newest first. With `?limit=N` only
(default 1000), returns raw results. With `?start=&end=&resolution=raw|minute|hour|day|auto`,
returns an aggregated **Series** of time buckets instead.

**Raw response:**
```json
{
  "ip": "8.8.8.8",
  "count": 2,
  "results": [
    { "ip": "8.8.8.8", "seq": 41, "timestamp": "2026-06-02T10:00:01Z", "success": true, "rttNanos": 12345678 },
    { "ip": "8.8.8.8", "seq": 40, "timestamp": "2026-06-02T10:00:00Z", "success": false, "rttNanos": 0, "errMsg": "error reading response: i/o timeout" }
  ]
}
```

### Extended endpoints (v1.2)

See [openapi.yml](./openapi.yml) for full request/response schemas.

| Method & path | Purpose |
|---------------|---------|
| `GET /api/hosts/full` | All hosts with config, tags, groups, alert thresholds |
| `GET /api/hosts/{ip}` | One host's full record |
| `PUT /api/hosts/{ip}/config` | Update per-IP interval/timeout/packetSize (restarts pinger) |
| `PUT /api/hosts/{ip}/meta` | Update displayName/notes/tags |
| `PUT /api/hosts/{ip}/alerts` | Update latency/loss alert thresholds |
| `GET /api/series?ips=a,b&start=&end=&resolution=` | Aggregated series for several hosts (overlay) |
| `GET /api/status?n=60` | NOC wall snapshot: per-host state, sparkline, breaches |
| `GET /api/hosts/{ip}/availability` | Uptime % over a range |
| `GET /api/hosts/{ip}/percentiles` | p50/p95/p99 + jitter over a range |
| `GET /api/hosts/{ip}/outages?minFails=3` | Detected outage periods |
| `GET/POST/PUT/DELETE /api/groups[...]` | Group CRUD, membership, `/health` rollup |
| `GET/POST /api/hosts/{ip}/annotations`, `PUT/DELETE /api/annotations/{id}` | Comments / incidents / maintenance windows |

### Legacy pinger endpoints

#### POST /api/pinger/add
Adds new IPs to monitor. Prefer `POST /api/hosts`.

**Request:**
```json
{
  "ips": ["192.168.1.1", "8.8.8.8"]
}
```

**Response:**
```json
{
  "success": true
}
```

#### POST /api/pinger/reset
Resets statistics for specific IPs. Prefer `POST /api/hosts/{ip}/reset`.

#### POST /api/pinger/remove
Removes pingers completely. Prefer `DELETE /api/hosts/{ip}`.

**Request:**
```json
{
  "ips": ["192.168.1.1"]
}
```

**Response:**
```json
{
  "success": true
}
```

## Starting the Server

The server requires root/administrator privileges to use ICMP ping:

```bash
sudo go run main.go
```

By default, the server listens on port 6868. You can access the web interface at http://localhost:6868.

### Command-Line Options

Run `pingmon --help` for a colorized summary of all flags with usage examples
(color is automatically disabled when output is piped or `NO_COLOR` is set).

Available flags:

```bash
sudo ./bin/pingmon \
  --port 8888 \
  --datafolder /var/lib/pingmon \
  --raw-retain 720h \
  --rollup-interval 5m
```

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `127.0.0.1` | Address to listen on: an IP, `0.0.0.0` (all), or an interface name (e.g. `eth0`) whose IP(s) are bound. **Repeatable / comma-separated, up to 3** |
| `--port` | `6868` | HTTP port (shared by all listen addresses) |
| `--config` | `/etc/pingmon.conf` | Config file (KEY=VALUE); see `pingmon config` |
| `--datafolder` | `/var/lib/pingmon` | Data directory (created if missing); DB is always `<datafolder>/pingmon.db` |
| `--readonly` | `false` | Read-only mode: block all writes (return `423`); for freezing data or a demo |
| `--arpscan` | `false` | Periodically scan the ARP table on all interfaces; exposes `GET /api/arp` |
| `--arp-interval` | `2m` | Passive ARP-cache read interval (or config `arp_interval`) |
| `--arp-active` | `true` | Actively ARP-sweep each subnet (arp-scan -l style; Linux+root, else passive) |
| `--arp-active-interval` | `10m` | How often the active sweep runs (or config `arp_active_interval`) |
| `--arp-active-max-hosts` | `256` | Skip active sweep of subnets larger than this (~/24) |
| `--min-ping-interval` | `500ms` | Enforced minimum ping interval; lower values are clamped up (or config `minimum_ping_interval`) |
| `--raw-retain` | `720h` | How long to keep raw results before pruning (`0` = forever) |
| `--rollup-interval` | `5m` | How often the background rollup/prune job runs |
| `--token` | _(none)_ | Bearer token (lowercase uuid4) authorizing API access; **repeatable** |
| `--debug` | `false` | Verbose logging: per-ping output, auth decisions, request headers, rollup timing |
| `--name` | _(OS hostname)_ | Instance name reported by `GET /api/ping` and `GET /api/profile` |

## Authentication (optional)

Token authentication is **off by default**. When started with one or more tokens,
the API requires a Bearer token; with no tokens it is fully open.

Generate a token with `pingmon token` (it prints a random lowercase uuid4).
Provide tokens via repeatable `--token` flags and/or the config file:

```bash
sudo ./bin/pingmon \
  --token 3f2504e0-4f89-41d3-9a0c-0305e82c3301 \
  --token a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d
```

```ini
# /etc/pingmon.conf  (comma-separated and/or repeated token= lines)
token=3f2504e0-4f89-41d3-9a0c-0305e82c3301,a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d
```

Each token must be a **lowercase uuid4**; the server refuses to start on an
invalid token. When any token is configured:

- Every `/api/*` endpoint requires `Authorization: Bearer <token>` **except**
  `GET /api/ping` and `GET /api/profile` (public).
- Unauthorized, missing, or invalid-token requests to protected endpoints return
  **403 Forbidden**. `404` is reserved for genuinely non-existent routes.

Comparisons are constant-time. Tokens are never logged. Keep your config file
out of version control if it contains tokens.

## Multi-backend / CORS

This server is a self-contained, independent backend. A single frontend can
register many PingMon servers (by URL, with an optional per-server token) and
talk to each one directly, aggregating results client-side — no cross-server
endpoint exists or is needed.

To support a browser UI hosted on a different origin connecting to arbitrary
backend URLs:

- **CORS is fully open** (any origin, method, and header — including
  `Authorization`) and preflight `OPTIONS` is answered. There are no CORS
  checks, so a browser UI on any origin can connect to any backend URL. This is
  safe because auth uses a bearer header (not cookies); protect the API with
  tokens and/or `--host`/network controls rather than CORS.
- **`GET /api/profile`** lets the UI fetch the instance name, version, and
  whether auth is required when adding a backend.
- A bad/missing token on a protected endpoint returns **`403`** (a wrong URL
  returns `404`), so the UI can distinguish "forbidden" from "not found".

## Read-only / demo mode

Start with `--readonly` to **freeze** all state: every mutating request
(`POST`/`PUT`/`PATCH`/`DELETE` under `/api`) is rejected with **`423 Locked`**
and a `{ "success": false, "readOnly": true }` body, while reads and live polling
continue to work. `GET /api/profile` reports `"readOnly": true` so a UI can
switch to view-only up front (rather than discovering it on the first failed
write). Use it to publish a frozen view of your hosts/groups, or to run a public
demo with realtime data that visitors cannot modify.

```bash
sudo ./bin/pingmon --readonly
```

## ARP discovery

With `--arpscan`, the server periodically reads the OS ARP/neighbor table on all
interfaces, resolves names (best-effort reverse DNS), and **accumulates** what it
sees — entries are never forgotten. Each entry tracks `firstSeen`, `lastSeen`,
and `count` (how many scans saw it); the latest MAC/interface is kept. Names are
re-resolved each scan and **every distinct name ever seen is kept in `names`** —
so a host whose reverse-DNS changes shows all of its names. Results are published
at `GET /api/arp` as a helper for the UI.

**Two discovery modes, used together:**

- **Passive** (always): reads the OS ARP/neighbor cache (`/proc/net/arp` on Linux,
  `arp -an` elsewhere) every `--arp-interval` (default `2m`). Only shows hosts the
  OS has recently talked to.
- **Active** (`--arp-active`, default on; **Linux + root**): broadcasts ARP
  "who-has" to every IP in each interface's IPv4 subnet — like `arp-scan -l` —
  discovering live hosts with no prior traffic. It runs every
  `--arp-active-interval` (default `10m`, separate from and slower than the
  passive read so background broadcast stays infrequent). A /24 is ~254 tiny
  packets in ~3 s. On macOS/non-root it **auto-falls back to passive** (logged
  once). Subnets larger than `--arp-active-max-hosts` (default `256`, ≈/24) are
  skipped (passive-only) to avoid flooding big networks.

```bash
# server: passive every 2m + active sweep every 10m (defaults)
sudo ./bin/pingmon --arpscan
sudo ./bin/pingmon --arpscan --arp-active-interval 30s --arp-active-max-hosts 1024

# one-shot CLI (active by default; needs root for the active sweep)
sudo pingmon arp                # active + passive, like arp-scan -l
pingmon arp --passive           # cache only, no broadcasts
```

The `GET /api/arp` response schema is unchanged; entries now include
active-discovery results in addition to the OS cache.

## Minimum ping interval

The server enforces a floor on per-host ping intervals: any configured interval
below the minimum is silently **clamped up** (the stored/returned value reflects
the enforced value, so the UI sees the truth). Default `500ms`, configurable via
`--min-ping-interval` or config `minimum_ping_interval`. The floor is advertised
as `minIntervalMs` in `GET /api/profile`.

## CLI commands

`pingmon` runs the server by default, but also provides standalone commands
(the command framework is built to grow). The store-backed commands operate
**directly on the data store — no server and no token required** — useful for
provisioning or inspecting a database offline.

```bash
# Print the version and exit
pingmon version          # also: pingmon --version, pingmon -v

# Generate a random API token (prints the token only)
pingmon token            # e.g. sudo pingmon --token "$(pingmon token)"

# ARP (one-shot scan, no server)
pingmon arp [--json] [--no-resolve]

# Effective config + snapshot, WITHOUT starting the server (great for diagnosing
# config issues — shows the real resolved listen address, paths, auth, etc.)
pingmon status [--json] [--config FILE]

# Stored-state summary (hosts, groups, results, db size, data span)
pingmon stats [--json] [--datafolder DIR]

# Hosts
pingmon host add [--interval ms] [--timeout ms] [--size n] [--name N] [--tags a,b] <ip> [<ip>...]
pingmon host list

# Groups
pingmon group add [--color C] [--desc D] <name>
pingmon group edit [--name N] [--color C] [--desc D] <id>
pingmon group list
pingmon group assign   <group-id> <ip> [<ip>...]
pingmon group unassign <group-id> <ip> [<ip>...]

# Config file (/etc/pingmon.conf)
pingmon config test [path]     # validate the config (strict; lists every problem)
sudo pingmon config set [path] # install the default config (prompts before overwrite)

# systemd service (Linux; requires root) — auto-detects the binary path and prompts
pingmon systemctl install      # write unit, then enable --now (prompts; --yes to skip)
pingmon systemctl uninstall    # stop, disable, and remove the unit (prompts)
pingmon systemctl enable       # enable + start on boot
pingmon systemctl disable      # disable + stop
pingmon systemctl status       # PingMon's view + `systemctl status pingmon`
pingmon systemctl --help       # systemctl subcommand help

pingmon help            # list commands and flags
```

`systemctl install` writes `/etc/systemd/system/pingmon.service` with
`ExecStart` set to the **auto-detected binary path** and `WorkingDirectory` to
the current directory, shows you the unit and asks for confirmation, then runs
`systemctl enable --now pingmon` (so it is **enabled on boot and started**).
It requires Linux + root; on other systems it refuses cleanly.

All store commands accept `--datafolder` (defaulting to the server's).
The enforced minimum ping interval still applies (a low `--interval` is clamped
up). Flags must come **before** positional arguments.

> A running server only loads hosts at startup, so hosts added/edited via the
> CLI while the server is running are picked up on its next restart.

## Config file

All settings live in a config file (default `/etc/pingmon.conf`, override with
`--config`) using simple `KEY=VALUE` syntax. Keys mirror the flags, plus
`token`, `arp_interval`, and `minimum_ping_interval`.

**Precedence:** command-line flag → config file → built-in default. Tokens from
flags and the config file are merged.

```ini
# /etc/pingmon.conf
host=0.0.0.0
port=6868
datafolder=/var/lib/pingmon
arpscan=true
arp_interval=2m
minimum_ping_interval=500ms
token=3f2504e0-4f89-41d3-9a0c-0305e82c3301
```

Manage it with the CLI:

```bash
sudo pingmon config set        # install the commented default to /etc/pingmon.conf
pingmon config test            # validate (strict — reports every problem at once)
```

Validation is **strict but graceful**: unknown keys, bad types/ranges, malformed
durations/booleans, duplicate keys, and non-uuid4 tokens are all caught and
listed together. A documented example ships at
[`etc/pingmon.conf`](./etc/pingmon.conf).

## Configuration

The server uses the following default configuration:

- **Config file**: `/etc/pingmon.conf` (`--config`), if present
- **Listen address**: 127.0.0.1 (`--host`; bind `0.0.0.0` to expose on the network, an interface name like `eth0`, or up to 3 comma-separated/repeated values)
- **Port**: 6868 (`--port`)
- **Data folder**: `/var/lib/pingmon` (`--datafolder`; auto-created) — holds the SQLite database (+ WAL files)
- **Database**: always `<datafolder>/pingmon.db`
- **Raw retention**: 720h (`--raw-retain`)
- **Rollup interval**: 5m (`--rollup-interval`)
- **Default ping config**: interval 1s, timeout 2s, packet size 56 bytes (per-host, overridable)
- **Minimum ping interval**: 500ms (`--min-ping-interval` / config `minimum_ping_interval`)
- **ARP scan**: off (`--arpscan`); interval `--arp-interval` / config `arp_interval`, default 2m
- **Number of Pings**: Continuous until stopped

## Security Considerations

The application requires root/administrator privileges due to the use of ICMP ping. This is a standard requirement for applications using raw sockets.

API authentication is **optional** and off by default (see [Authentication](#authentication-optional)). When tokens are configured, all `/api/*` endpoints except `GET /api/ping` require a Bearer token and unauthorized requests receive a `404`. With no tokens configured the API is open and the application is intended for trusted, internal-network use only. There is no per-user authorization model — any valid token grants full API access. Terminate TLS at a reverse proxy for deployments exposed beyond localhost.

## Logging

On startup the server prints a colorized overview — name, listen address, data
folder, database path, number of monitored hosts, auth status (with masked
tokens), CORS origins, retention/rollup, and debug state.

Log lines are timestamped and colorized (color auto-disables when output is
piped or `NO_COLOR` is set). By default the server logs concisely: host
lifecycle events (start/stop/resume), one line per HTTP request (status colored
by class, method, path, duration), rollup prune actions, and errors.

Run with `--debug` for a verbose firehose suitable for troubleshooting:

- Per-ping send/result lines and per-host `[PING-STATS]` summaries
- Incoming-request lines with User-Agent, content length, and whether a Bearer token was present
- Auth decisions (`auth: allowed/DENIED <path>`) — tokens themselves are never logged
- Rollup job timing per tick

```bash
sudo go run main.go --debug
```
