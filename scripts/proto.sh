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

# Drift checks: catch .speckdl ↔ .proto schema drift before we regenerate.
# Protocol layer.
scripts/drift-check.sh
# Product layer (cycle 73).
scripts/drift-check-product.sh

buf format -w
buf generate