FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/vpsbill ./cmd/server \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/keyrotate ./cmd/keyrotate
# Every API image carries the matching Hatch agent for both node
# architectures, served at /api/v1/agent/download/.
COPY deploy/install-hatch-agent.sh /out/hatch-agent/install.sh
COPY deploy/hatch-agent.service /out/hatch-agent/hatch-agent.service
RUN for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w" -o /out/hatch-agent/hatch-agent-linux-$arch ./cmd/hatch-agent; \
    done \
    && cd /out/hatch-agent && sha256sum hatch-agent-linux-* > SHA256SUMS

FROM alpine:3.23
# Keep trust roots and timezone data on the security patch level shipped by the
# selected Alpine base instead of pinning repository revisions that disappear.
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build /out/vpsbill /app/vpsbill
COPY --from=build /out/keyrotate /app/keyrotate
COPY --from=build /out/hatch-agent /app/hatch-agent
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/vpsbill"]
