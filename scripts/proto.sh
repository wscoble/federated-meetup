#!/usr/bin/env bash
# Generate Go code from proto/ using buf + protoc-gen-go + protoc-gen-connect-go.
# Requires: buf, protoc-gen-go, protoc-gen-connect-go, protoc-gen-validate.
#
# Install:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
#   go install github.com/envoyproxy/protoc-gen-validate@latest
#   nix profile install nixpkgs#buf nixpkgs#protobuf
#
# Also runs scripts/drift-check.sh to verify the handwritten .proto
# matches what specs/FederatedMeetup.speckdl would emit.
set -euo pipefail

cd "$(dirname "$0")/.."

# Drift check: catch .speckdl ↔ .proto schema drift before we regenerate.
scripts/drift-check.sh

buf format -w
buf generate