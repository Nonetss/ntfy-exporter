# ntfy-exporter

Small Go service that keeps one long-lived [ntfy](https://ntfy.sh/) JSON stream open per topic and forwards events to [Grafana Loki](https://grafana.com/docs/loki/) using `POST /loki/api/v1/push`.

By default only `message` events are sent; `open` and `keepalive` are skipped unless you enable full export (see environment variables).

## Grafana and dashboards

Importing the dashboard JSON **by itself does nothing useful**. This dashboard expects logs in **Loki** that were produced by the **ntfy → Loki exporter**. You therefore need:

1. **Loki** receiving pushes from the exporter (same project).
2. **The ntfy exporter** from **[github.com/Nonetss/ntfy-exporter](https://github.com/Nonetss/ntfy-exporter)** — source code, Docker image, and `compose.yml` are all in that repo. Run it against your ntfy server so ntfy events show up in Loki (default stream label `job="ntfy"`).
3. **Grafana** with a **Loki data source** aimed at that Loki instance.
4. Then import the dashboard files from the **`dashboard/`** folder in the same repository.

| File | Use case |
|------|----------|
| `ntfy-dashboard.json` | **Grafana 13+** schema-style export (recommended for current Grafana). **Dashboards → Import → Upload JSON**. |
| `ntfy-dashboard.yml` | YAML for dashboards-as-code workflows. |

After import, choose your **Loki** data source. Panel queries use `{job="ntfy"}` (or whatever you set via `LOKI_JOB`) and parse JSON fields such as `message`, `title`, `priority`, and `topic`.

## Requirements

- An ntfy server reachable over HTTP(S).
- Loki accepting push requests at the base URL you configure (no path suffix; the binary appends `/loki/api/v1/push`).

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `NTFY_URL` or `NTFY_BASE_URL` | Yes | ntfy base URL, no trailing slash (e.g. `https://ntfy.example.com`). |
| `NTFY_TOPICS` | Yes | Comma-separated topics (e.g. `alerts,home`). |
| `LOKI_URL` | Yes | Loki base URL (e.g. `http://localhost:3100`). |
| `LOKI_JOB` | No | Loki `job` label. Default: `ntfy`. |
| `NTFY_EXPORT_ALL_EVENTS` | No | If `1`, `true`, `yes`, or `on`, also forwards `open` and `keepalive`. |
| `NTFY_PRINT_TITLE_FIGURE` | No | If `1`, `true`, `yes`, or `on`, renders ASCII for each `message` event to stdout (`docker compose logs`) **and** adds **`figure_ascii`** (multiline string) to the JSON pushed to Loki. Uses **title** when set; otherwise the **first line of `message`**. Text longer than 80 runes is truncated. |
| `NTFY_TITLE_FIGURE_RENDERER` | No | `gofigure` (default): [go-figure](https://github.com/common-nighthawk/go-figure) FIGlet fonts via `NTFY_TITLE_FIGURE_FONT`. `blocklet`: [blocklet](https://github.com/tanav-malhotra/blocklet) Unicode █ art using the **`blocklet` CLI** (Rust — use `cargo install` or the exporter Docker image; **not** `go install`). |
| `NTFY_TITLE_FIGURE_FONT` | No | go-figure only: font name without `.flf`. Empty uses `standard`. Unknown fonts fall back to `standard` with a log warning. |
| `NTFY_BLOCKLET_BIN` | No | blocklet only: path to the `blocklet` binary; default `blocklet` on `PATH`. Docker image installs `/usr/local/bin/blocklet`. |
| `NTFY_BLOCKLET_FONT` | No | blocklet only: `standard`, `standard_shadow`, or `standard_solid`. **Empty** runs `blocklet --no-shadow` (solid blocks, typical █ grid). |
| `LOKI_TENANT_ID` | No | Sets `X-Scope-OrgID` for multi-tenant Loki. |
| `LOKI_BASIC_AUTH_USER` / `LOKI_BASIC_AUTH_PASSWORD` | No | Basic auth toward Loki. |

Each log line stored in Loki is JSON: all ntfy event fields, plus **`figure_ascii`** when `NTFY_PRINT_TITLE_FIGURE` is enabled and a phrase was rendered. Stream labels include `job`, `topic`, `source=ntfy`, and `event`, plus `priority` when present.

## Run locally (Go)

```bash
cp .env.example .env
# Edit .env

go run ./cmd/main
```

Or build a binary:

```bash
go build -o ntfy-exporter ./cmd/main
./ntfy-exporter
```

## Docker

```bash
docker build -t ntfy-exporter:local .
docker run --rm -e NTFY_URL=... -e NTFY_TOPICS=... -e LOKI_URL=... ntfy-exporter:local
```

CI can publish an image to GitHub Container Registry (`ghcr.io/<user>/<repo>`). The included `compose.yml` may reference a specific image; change it to your fork or use `build: .` for a local build.

## Docker Compose

```bash
cp .env.example .env
# Set NTFY_URL, NTFY_TOPICS, LOKI_URL

docker compose up -d
```

Optional settings from `.env.example` are passed through in `compose.yml`, including figure rendering (`NTFY_PRINT_TITLE_FIGURE`, `NTFY_TITLE_FIGURE_RENDERER`, `NTFY_TITLE_FIGURE_FONT`, `NTFY_BLOCKLET_*`). The multi-stage **Dockerfile** builds [blocklet](https://github.com/tanav-malhotra/blocklet) `v0.1.3` and installs it beside `ntfy-exporter`; set `NTFY_TITLE_FIGURE_RENDERER=blocklet` to use it.

If figures never appear, your registry image may predate this feature: build locally (`docker build -t ntfy-exporter:local .` and point `compose.yml` at that image, or add `build: .` under the service) so the binary matches this repo.

If Loki runs on the host and you set `LOKI_URL=http://host.docker.internal:3100`, on Linux you often need this in `compose.yml`:

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

## Example LogQL

```logql
{job="ntfy"}
```

```logql
{job="ntfy", topic="alerts"} | json | line_format "{{.message}}"
```

```logql
{job="ntfy"} | json | figure_ascii != "" | line_format "{{.figure_ascii}}"
```

## Reconnect behaviour

If the ntfy stream drops, the client reconnects per topic with exponential backoff up to 30 seconds between attempts.
