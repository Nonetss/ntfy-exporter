# ntfy-exporter

Servicio en Go que mantiene abiertos los streams JSON de [ntfy](https://ntfy.sh/) por cada topic configurado y reenvía los eventos a [Grafana Loki](https://grafana.com/docs/loki/) mediante la API `POST /loki/api/v1/push`.

Por defecto solo se exportan eventos `message`; `open` y `keepalive` se omiten salvo que actives el modo explícito (ver variables de entorno).

## Requisitos

- Un servidor ntfy accesible por HTTP(S).
- Una instancia de Loki que acepte pushes en la URL base que indiques (sin path final; el binario añade `/loki/api/v1/push`).

## Variables de entorno

| Variable | Obligatoria | Descripción |
|----------|-------------|-------------|
| `NTFY_URL` o `NTFY_BASE_URL` | Sí | URL base del servidor ntfy, sin barra final (p. ej. `https://ntfy.example.com`). |
| `NTFY_TOPICS` | Sí | Lista de topics separados por comas (p. ej. `alertas,home`). |
| `LOKI_URL` | Sí | URL base de Loki (p. ej. `http://localhost:3100`). |
| `LOKI_JOB` | No | Etiqueta `job` en Loki. Por defecto: `ntfy`. |
| `NTFY_EXPORT_ALL_EVENTS` | No | Si es `1`, `true`, `yes` u `on`, también se envían `open` y `keepalive`. |
| `LOKI_TENANT_ID` | No | Valor de la cabecera `X-Scope-OrgID` (Loki multi-tenant). |
| `LOKI_BASIC_AUTH_USER` / `LOKI_BASIC_AUTH_PASSWORD` | No | Basic auth hacia Loki. |

Cada línea enviada a Loki es el JSON completo del evento ntfy. Las etiquetas del stream incluyen `job`, `topic`, `source=ntfy` y `event`.

## Ejecución local (Go)

```bash
cp .env.example .env
# Edita .env

go run ./cmd/main
```

O compilar:

```bash
go build -o ntfy-exporter ./cmd/main
./ntfy-exporter
```

## Docker

```bash
docker build -t ntfy-exporter:local .
docker run --rm -e NTFY_URL=... -e NTFY_TOPICS=... -e LOKI_URL=... ntfy-exporter:local
```

En CI se publica una imagen en GitHub Container Registry (`ghcr.io/<usuario>/<repo>`). El `compose.yml` del repo apunta a una imagen concreta: cámbiala por la de tu fork o vuelve a `build: .` si prefieres construir en local.

## Docker Compose

```bash
cp .env.example .env
# Ajusta NTFY_URL, NTFY_TOPICS y LOKI_URL

docker compose up -d
```

Si Loki corre en el host y usas `LOKI_URL=http://host.docker.internal:3100`, en Linux suele hace falta descomentar en `compose.yml`:

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

## Consultas en LogQL (ejemplo)

```logql
{job="ntfy"}
```

```logql
{job="ntfy", topic="alertas"} | json | line_format "{{.message}}"
```

## Comportamiento ante cortes

Si el stream HTTP se cierra, el cliente reabre la conexión con espera exponencial hasta 30 s entre reintentos, por topic.
