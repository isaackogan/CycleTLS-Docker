# CycleTLS-Docker

A minimal Docker wrapper around [CycleTLS](https://github.com/Danny-Dasilva/CycleTLS) that runs the Go binary and exposes its native WebSocket server on port 9112.

This repository exists to make CycleTLS usable as a standalone, language-agnostic service. Instead of pulling in the `cycletls` npm package and spawning the binary from Node, you can run this image and connect to `ws://localhost:9112/` from any language that speaks WebSockets.

Latest Release: `28f3d8b`

## Usage

```bash
docker run --rm -p 127.0.0.1:9112:9112 ghcr.io/isaackogan/cycletls-docker:<cycletls-git-ref>
```

The image tag corresponds to the upstream CycleTLS git ref (commit hash or tag) the binary was built against.

## Health check

`GET /healthz` returns `200 OK` with body `ok`. Suitable for a Kubernetes `httpGet` liveness or readiness probe on port 9112.

## Wire protocol

Requests are JSON: `{ "requestId": "...", "options": { ... } }`. Responses are CycleTLS's native length-prefixed binary frames (`response`, `data`, `chunk`, `end`, `error`, `redirect`), one logical request emitting multiple frames keyed by `requestId`. See `test/e2e_test.go` for a minimal decoder.

## Repository layout

- `src/` Go source for the WebSocket server binary
- `test/` E2E test that exercises a running container
- `Dockerfile` multi-stage build, accepts `CYCLETLS_REF` build arg
- `.github/workflows/publish.yml` builds, tests, and pushes to GHCR on manual dispatch

## Security note

The server is unauthenticated. Bind it to `127.0.0.1` only, or place it behind your own access control.
