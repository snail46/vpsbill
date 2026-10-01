# lxcfs for Podman hosts: Debian 12 and Ubuntu 22.04/24.04 ship 5.0.x, which
# cannot see swap limits on cgroup v2. Built on Ubuntu 22.04 (glibc 2.35,
# libfuse3.so.3) so it runs on all of them; hosts whose own lxcfs is 6 or
# newer keep theirs (see deploy/install-hatch-agent.sh). arm64 builds under
# emulation.
ARG LXCFS_VERSION=6.0.5

FROM --platform=linux/amd64 ubuntu:22.04 AS lxcfs-amd64
ARG LXCFS_VERSION
COPY deploy/build-lxcfs.sh /build-lxcfs.sh
RUN sh /build-lxcfs.sh "$LXCFS_VERSION" amd64

FROM --platform=linux/arm64 ubuntu:22.04 AS lxcfs-arm64
ARG LXCFS_VERSION
COPY deploy/build-lxcfs.sh /build-lxcfs.sh
RUN sh /build-lxcfs.sh "$LXCFS_VERSION" arm64

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# VERSION (the commit, passed by CI) is what the site and the agent report.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/vpsbill ./cmd/server \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/keyrotate ./cmd/keyrotate
# Every API image carries the matching Hatch agent for both node
# architectures, served at /api/v1/agent/download/.
COPY deploy/install-hatch-agent.sh /out/hatch-agent/install.sh
COPY deploy/hatch-agent.service /out/hatch-agent/hatch-agent.service
COPY --from=lxcfs-amd64 /hatch-lxcfs-linux-amd64.tar.gz /out/hatch-agent/
COPY --from=lxcfs-arm64 /hatch-lxcfs-linux-arm64.tar.gz /out/hatch-agent/
RUN for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/hatch-agent/hatch-agent-linux-$arch ./cmd/hatch-agent; \
    done \
    && cd /out/hatch-agent && sha256sum hatch-agent-linux-* hatch-lxcfs-linux-* > SHA256SUMS

FROM alpine:3.23
# Keep trust roots and timezone data on the security patch level shipped by the
# selected Alpine base instead of pinning repository revisions that disappear.
# hadolint ignore=DL3018
# The PostgreSQL clients make and restore backups; several majors are there
# so the one matching the database server is used.
RUN apk add --no-cache ca-certificates tzdata postgresql16-client postgresql17-client postgresql18-client \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && install -d -m 0700 -o app -g app /var/lib/vpsbill/backups
WORKDIR /app
COPY --from=build /out/vpsbill /app/vpsbill
COPY --from=build /out/keyrotate /app/keyrotate
COPY --from=build /out/hatch-agent /app/hatch-agent
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/vpsbill"]
