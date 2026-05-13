# ntfy-exporter

Small Go service that keeps one long-lived [ntfy](https://ntfy.sh/) JSON stream open per topic and forwards events to [Grafana Loki](https://grafana.com/docs/loki/) using `POST /loki/api/v1/push`.

By default only `message` events are sent; `open` and `keepalive` are skipped unless you enable full export (see environment variables).

## Grafana and dashboards

To **visualize** these logs in Grafana you need:

1. **This exporter** (or equivalent) writing to Loki with label `job` matching your setup (default `ntfy`).
2. A **Loki data source** in Grafana pointed at the same Loki instance.
3. A **dashboard** that queries those logs.

**Official dashboards and import files live in this repository:**  
[https://github.com/Nonetss/ntfy-exporter](https://github.com/Nonetss/ntfy-exporter)

In the [`dashboard/`](https://github.com/Nonetss/ntfy-exporter/tree/main/dashboard) folder you will find:

| File | Use case |
|------|----------|
| `ntfy-dashboard.json` | **Grafana 13+** schema-style export (recommended for current Grafana). Import via **Dashboards → Import → Upload JSON**. |
| `ntfy-dashboard.grafana.com.json` | **Classic** JSON (string datasources, `__inputs` for Loki). Use for [grafana.com dashboards](https://grafana.com/grafana/dashboards/) uploads or older Grafana versions. |
| `ntfy-dashboard.yml` | YAML equivalent of the schema dashboard (if you manage dashboards as code). |

After import, map the **Loki** data source when prompted. Queries assume streams like `{job="ntfy"}` and JSON log lines from ntfy events (`message`, `title`, `priority`, `topic`, etc., depending on what ntfy sends).

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
| `LOKI_TENANT_ID` | No | Sets `X-Scope-OrgID` for multi-tenant Loki. |
| `LOKI_BASIC_AUTH_USER` / `LOKI_BASIC_AUTH_PASSWORD` | No | Basic auth toward Loki. |

Each log line stored in Loki is the full ntfy event JSON. Stream labels include `job`, `topic`, `source=ntfy`, and `event`, plus `priority` when present.

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

## Reconnect behaviour

If the ntfy stream drops, the client reconnects per topic with exponential backoff up to 30 seconds between attempts.
