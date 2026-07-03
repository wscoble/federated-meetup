# Dockerfile for the federated-meetup host daemon.
# Serves ConnectRPC + MCP + web UI on a single port.
#
# Build:  docker build -t fedmeetup -f Dockerfile .
# Run:    docker run -p 8091:8091 -e HOSTD_GROUP_KEY="0x$(python3 -c 'print("aa"*32)')" fedmeetup

FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /fedmeetup ./cmd/fedmeetup

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /fedmeetup /usr/local/bin/fedmeetup

ENV HOSTD_ADDR=":8091"
EXPOSE 8091

ENTRYPOINT ["fedmeetup"]