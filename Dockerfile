# syntax=docker/dockerfile:1

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
USER ntfy
ENTRYPOINT ["/usr/local/bin/ntfy-exporter"]
