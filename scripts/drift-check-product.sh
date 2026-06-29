#!/usr/bin/env bash
# scripts/drift-check-product.sh
#
# Drift check for the product-layer spec (FederatedMeetupProduct.speckdl)
# against the handwritten proto/federated_meetup/product/v1/{state,rpc}.proto.
#
# Same pattern as drift-check.sh (protocol layer) but for the product layer.
# The handwritten .proto is the source of truth; the .speckdl is a CI drift gate.
#
# Usage:
#   scripts/drift-check-product.sh
#   scripts/drift-check-product.sh --strict
#
# Requires: speckl-compile (from ~/speckl/compiler), protoc, node.

set -euo pipefail
cd "$(dirname "$0")/.."

SPECKL_BIN="${SPECKL_BIN:-$HOME/speckl/compiler/dist/index.js}"
HAND_STATE="proto/federated_meetup/product/v1/state.proto"
HAND_RPC="proto/federated_meetup/product/v1/rpc.proto"
SPECKDL="specs/FederatedMeetupProduct.speckdl"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

STRICT=0
for arg in "$@"; do
    case "$arg" in
        --strict) STRICT=1 ;;
        --help|-h)
            sed -n '2,/^set -euo/p' "$0" | head -40
            exit 0
            ;;
        -*) echo "drift-check-product: unknown flag $arg" >&2; exit 2 ;;
    esac
done

if [[ ! -f "$SPECKL_BIN" ]]; then
    echo "drift-check-product: speckl compiler not found at $SPECKL_BIN" >&2
    exit 2
fi
if [[ ! -f "$SPECKDL" ]]; then
    echo "drift-check-product: $SPECKDL not found" >&2
    exit 2
fi

echo "drift-check-product: compiling $SPECKDL via speckl -> $TMP/federated_meetup_product.proto"
node "$SPECKL_BIN" "$SPECKDL" --output-dir "$TMP" --target protobuf >/dev/null
SPECKL_PROTO="$TMP/federated_meetup_product.proto"

echo "drift-check-product: protoc --descriptor_set_out check"
if ! protoc --proto_path="$TMP" --descriptor_set_out=/dev/null "$SPECKL_PROTO" 2>"$TMP/protoc.err"; then
    echo "  FAIL: protoc rejected speckl-emitted proto" >&2
    cat "$TMP/protoc.err" >&2
    exit 1
fi
echo "  ok: protoc accepted the emitted proto"

# Extract all message/enum/service declarations from handwritten protos.
extract_hand_names() {
    awk '
        /^message[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ { gsub(/[ \t{]+$/, "", $2); print "message:" $2 }
        /^enum[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/    { gsub(/[ \t{]+$/, "", $2); print "enum:"    $2 }
        /^service[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/  { gsub(/[ \t{]+$/, "", $2); print "service:" $2 }
    ' "$HAND_STATE" "$HAND_RPC" | sort -u
}

HAND_NAMES="$(extract_hand_names)"

# Extract speckl-emitted declarations.
SPECKL_NAMES="$(awk '
    /^message[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ { gsub(/[ \t{]+$/, "", $2); print "message:" $2 }
    /^enum[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/    { gsub(/[ \t{]+$/, "", $2); print "enum:"    $2 }
    /^service[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/  { gsub(/[ \t{]+$/, "", $2); print "service:" $2 }
' "$SPECKL_PROTO" | sort -u)"

FAIL=0

# 1) Speckl-declared messages/enums must exist in handwritten.
MISSING="$(comm -23 <(echo "$SPECKL_NAMES") <(echo "$HAND_NAMES") || true)"
if [[ -n "$MISSING" ]]; then
    echo "  FAIL: speckl declares entries not in handwritten proto:" >&2
    echo "$MISSING" | sed 's/^/    /' >&2
    FAIL=1
fi

# 2) In --strict mode, also flag handwritten-only declarations.
if [[ $STRICT -eq 1 ]]; then
    HAND_ONLY="$(comm -13 <(echo "$SPECKL_NAMES") <(echo "$HAND_NAMES") || true)"
    if [[ -n "$HAND_ONLY" ]]; then
        echo "  FAIL (strict): handwritten proto declares entries not in speckl:" >&2
        echo "$HAND_ONLY" | sed 's/^/    /' >&2
        FAIL=1
    fi
fi

# Summary
COUNT="$(echo "$SPECKL_NAMES" | wc -l)"
echo "  ok: $COUNT declarations in speckl match handwritten proto"

if [[ $FAIL -ne 0 ]]; then
    echo "drift-check-product: FAIL"
    exit 1
fi
echo "drift-check-product: PASS"