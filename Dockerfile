# syntax=docker/dockerfile:1

# blocklet's transitive deps (e.g. clap 4.6+) need Cargo/rustc >= 1.85 (edition2024).
FROM rust:1.85-alpine AS blocklet
RUN apk add --no-cache musl-dev git \
	&& cargo install --git https://github.com/tanav-malhotra/blocklet \
		--rev v0.1.2 \
		--root /opt/blocklet

FROM golang:1.26-alpine AS build
WORKDIR /src
# go.mod puede pedir una versión más nueva que la de la imagen; descarga la toolchain.
ENV GOTOOLCHAIN=auto

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ntfy-exporter ./cmd/main

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
	&& adduser -D -H -u 65532 ntfy
COPY --from=build /out/ntfy-exporter /usr/local/bin/ntfy-exporter
COPY --from=blocklet /opt/blocklet/bin/blocklet /usr/local/bin/blocklet
USER ntfy
ENTRYPOINT ["/usr/local/bin/ntfy-exporter"]
