# Dockerfile for the federated-meetup host daemon.
# Serves ConnectRPC + MCP + web UI on a single port.
#
# Build:  docker build -t fedmeetup -f Dockerfile .
# Run:    docker run -p 8080:8080 -e HOSTD_GROUP_KEY="0x$(openssl rand -hex 32)" fedmeetup

FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /fedmeetup ./cmd/fedmeetup

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 65532 -S app && \
    adduser -u 65532 -S app -G app

COPY --from=builder /fedmeetup /usr/local/bin/fedmeetup

# Data directory for SQLite databases (protocol log + web store).
# Mount a volume here in production.
RUN mkdir -p /data && chown app:app /data

USER app

ENV HOSTD_ADDR=":8080"
ENV HOSTD_DB_PATH="/data/fedmeetup.db"
ENV HOSTD_PROTOCOL_DB="/data/protocol.db"
EXPOSE 8080

ENTRYPOINT ["fedmeetup"]