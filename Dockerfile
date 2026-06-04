FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ARG CYCLETLS_REF=""
ARG TARGETOS
ARG TARGETARCH

# git is required for `go get <module>@<commit-hash>`, which resolves the
# module via VCS rather than the module proxy.
RUN apk add --no-cache git

WORKDIR /src

COPY src/ ./

RUN if [ -n "$CYCLETLS_REF" ]; then \
      go get github.com/Danny-Dasilva/CycleTLS/cycletls@${CYCLETLS_REF} && \
      go mod tidy; \
    fi && \
    go mod download

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w" \
    -o /out/cycletls \
    .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/cycletls /cycletls

ENV WS_PORT=9112
EXPOSE 9112

USER nonroot:nonroot
ENTRYPOINT ["/cycletls"]
