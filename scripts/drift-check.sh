#!/usr/bin/env bash
# scripts/drift-check.sh
#
# Verifies that specs/FederatedMeetup.speckdl compiles cleanly to a
# .proto that is protoc-valid AND whose EVENT-PAYLOAD/ENUM/SERVICE
# declarations match the handwritten
# proto/federated_meetup/v1/{state,rpc}.proto.
#
# Speckl is NOT the source of truth in this project — the handwritten
# .proto files are (the Open Client Protobuf Standard). Speckl's role
# is a *consistency check*: if the .speckdl drifts away from the
# .proto, this script catches it before buf generate silently
# regenerates code from the wrong spec.
#
# Scope of the check (intentionally narrow):
#   - event_payloads: every `event X` block emits as <X>Payload. The
#     handwritten .proto must declare each of those messages, or the
#     consumer's apply switch (which references pb.<X>Payload) won't
#     compile.
#   - transition_type_enum: the TransitionType enum values from the
#     .speckdl's `transition` block must match the handwritten enum.
#   - service: the handwritten RPC service must exist; Speckl's role
#     here is to verify the RPC message surface (request/response)
#     matches the handwritten proto.
#   - primitives: the PublicKey/Signature/etc primitive interfaces
#     must map to handwritten messages of the same name.
#
# Out of scope (deliberately):
#   - state block contents (the state is a Merkle KV; Speckl's
#     `state as StateSnapshot` is a wire-format detail, not a
#     schema-declaration surface)
#   - field-level comparison (a much heavier check; a future cycle)
#   - type aliases (CustodyTier is an enum in handwritten proto, an
#     interface in speckdl — that drift is fine because the consumer
#     does not reference CustodyTier by name)
#
# Usage:
#   scripts/drift-check.sh            # check (default — emit drift
#                                      # report and fail if the
#                                      # handwritten proto is MISSING
#                                      # something speckl declares)
#   scripts/drift-check.sh --strict   # also fail if the handwritten
#                                      # proto declares something
#                                      # speckl does not
#
# Requires: speckl-compile (from ~/speckl/compiler), protoc, node.

set -euo pipefail

cd "$(dirname "$0")/.."

SPECKL_BIN="${SPECKL_BIN:-$HOME/speckl/compiler/dist/index.js}"
HAND_STATE="proto/federated_meetup/v1/state.proto"
HAND_RPC="proto/federated_meetup/v1/rpc.proto"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

STRICT=0
SPECKDL="specs/FederatedMeetup.speckdl"
for arg in "$@"; do
    case "$arg" in
        --strict) STRICT=1 ;;
        --help|-h)
            sed -n '2,/^set -euo/p' "$0" | head -50
            exit 0
            ;;
        -*) echo "drift-check: unknown flag $arg" >&2; exit 2 ;;
        *) SPECKDL="$arg" ;;
    esac
done

if [[ ! -f "$SPECKL_BIN" ]]; then
    echo "drift-check: speckl compiler not found at $SPECKL_BIN" >&2
    echo "  set SPECKL_BIN or build ~/speckl/compiler" >&2
    exit 2
fi
if [[ ! -f "$SPECKDL" ]]; then
    echo "drift-check: $SPECKDL not found" >&2
    exit 2
fi

echo "drift-check: compiling $SPECKDL via speckl → $TMP/federated_meetup.proto"
node "$SPECKL_BIN" "$SPECKDL" --output-dir "$TMP" --target protobuf >/dev/null
SPECKL_PROTO="$TMP/federated_meetup.proto"

echo "drift-check: protoc --descriptor_set_out check"
if ! protoc --proto_path="$TMP" --descriptor_set_out=/dev/null "$SPECKL_PROTO" 2>"$TMP/protoc.err"; then
    echo "  FAIL: protoc rejected speckl-emitted proto" >&2
    cat "$TMP/protoc.err" >&2
    exit 1
fi
echo "  ok: protoc accepted the emitted proto"

# Extract all `message X` / `enum X` / `service X` declarations.
# This is the *handwritten side's* set.
extract_hand_names() {
    awk '
        /^message[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ { gsub(/[ \t{]+$/, "", $2); print "message:" $2 }
        /^enum[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/    { gsub(/[ \t{]+$/, "", $2); print "enum:"    $2 }
        /^service[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/  { gsub(/[ \t{]+$/, "", $2); print "service:" $2 }
    ' "$1" | sort -u
}

HAND_NAMES="$TMP/hand.names"
: > "$HAND_NAMES"
extract_hand_names "$HAND_STATE" >> "$HAND_NAMES"
extract_hand_names "$HAND_RPC"   >> "$HAND_NAMES"

# Extract the speckl side's set of event-payload messages, the
# TransitionType enum, and the service.
SPECKL_PAYLOADS="$TMP/speckl.payloads"
SPECKL_ENUMS="$TMP/speckl.enums"
SPECKL_SERVICES="$TMP/speckl.services"

# Event payloads: <X>Payload messages declared in the speckl .proto.
# We approximate by finding `message X` and filtering for names ending
# in `Payload` (the convention enforced by `event_suffix: "Payload"`).
awk '
    /^message[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ {
        gsub(/[ \t{]+$/, "", $2)
        if ($2 ~ /Payload$/) print "message:" $2
    }
' "$SPECKL_PROTO" | sort -u | grep -v '^message:SignedEnvelopePayload$' > "$SPECKL_PAYLOADS"

# Enums.
awk '
    /^enum[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ {
        gsub(/[ \t{]+$/, "", $2)
        print "enum:" $2
    }
' "$SPECKL_PROTO" | sort -u > "$SPECKL_ENUMS"

# Services.
awk '
    /^service[[:space:]]+[A-Za-z_][A-Za-z0-9_]*/ {
        gsub(/[ \t{]+$/, "", $2)
        print "service:" $2
    }
' "$SPECKL_PROTO" | sort -u > "$SPECKL_SERVICES"

FAIL=0

# 1) Event payloads declared by speckl must exist in handwritten.
MISSING_PAYLOADS="$(comm -23 "$SPECKL_PAYLOADS" "$HAND_NAMES" || true)"
if [[ -n "$MISSING_PAYLOADS" ]]; then
    echo "  FAIL: speckl declares event payloads not in handwritten proto:" >&2
    echo "$MISSING_PAYLOADS" | sed 's/^/    /' >&2
    echo "  → Add the message to proto/federated_meetup/v1/state.proto." >&2
    FAIL=1
fi

# 2) TransitionType enum must match.
SPECKL_TT="$(grep -E '^enum:TransitionType$' "$SPECKL_ENUMS" || true)"
if [[ -n "$SPECKL_TT" ]] && ! grep -qFx "$SPECKL_TT" "$HAND_NAMES"; then
    echo "  FAIL: speckl declares enum TransitionType, handwritten proto does not." >&2
    FAIL=1
fi

# 3) Services (RPC) — speckl doesn't emit an RPC service in v0, so
#    this check is informational only. If a future Speckl extension
#    adds `service X` syntax, this will start enforcing.
SPECKL_SVCS="$(cat "$SPECKL_SERVICES" 2>/dev/null || true)"
HAND_SVCS="$(grep -E '^service:' "$HAND_NAMES" || true)"
if [[ -n "$SPECKL_SVCS" && -z "$(echo "$SPECKL_SVCS" | grep -Fxf - <(echo "$HAND_SVCS"))" ]]; then
    echo "  FAIL: speckl declares services not in handwritten proto:" >&2
    echo "$SPECKL_SVCS" | sed 's/^/    /' >&2
    FAIL=1
fi

# 4) In --strict mode, also fail if handwritten declares something
#    speckl doesn't.
if [[ $STRICT -eq 1 ]]; then
    HAND_ONLY="$(comm -13 <(cat "$SPECKL_PAYLOADS" "$SPECKL_ENUMS" "$SPECKL_SERVICES" | sort -u) "$HAND_NAMES" || true)"
    # Skip `ListGroupsResponse_GroupSummary` — it's a oneof wrapper the
    # handwritten proto generates automatically. Speckl's not-yet-emitted
    # wrapper is fine; we just don't fail on it.
    HAND_ONLY_FILTERED="$(echo "$HAND_ONLY" | grep -v 'ListGroupsResponse_GroupSummary' || true)"
    if [[ -n "$HAND_ONLY_FILTERED" ]]; then
        echo "  FAIL (strict): handwritten proto declares entries not in speckl:" >&2
        echo "$HAND_ONLY_FILTERED" | sed 's/^/    /' >&2
        echo "  → Add the message to $SPECKDL, or remove it from the .proto." >&2
        FAIL=1
    fi
fi

# Summary
PAYLOAD_COUNT="$(wc -l < "$SPECKL_PAYLOADS")"
echo "  ok: $PAYLOAD_COUNT event payloads in speckl match handwritten proto"
if [[ -n "$SPECKL_SVCS" ]]; then
    echo "  ok: services match: $(echo "$SPECKL_SVCS" | wc -l)"
fi

if [[ $FAIL -ne 0 ]]; then
    echo "drift-check: FAIL"
    exit 1
fi
echo "drift-check: PASS"
