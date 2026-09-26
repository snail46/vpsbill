FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/vpsbill ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/keyrotate ./cmd/keyrotate

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
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/vpsbill"]
