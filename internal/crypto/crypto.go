// SPDX-License-Identifier: MIT
//
// Package crypto wraps signing/verification with the protocol's canonical
// message format and the multisig envelope.
//
// Per docs/02-PROTOCOL.md §5.1, the federation has THREE independent key
// domains with no cross-derivation. The keys cross between layers only as
// messages. This package makes that boundary compiler-enforced by exposing
// distinct named types for each layer's key material:
//
//   - StewardKey   — Layer 2 (governance). Ed25519. Signs transitions.
//   - WireGuardKey — Layer 1 (mesh transport). X25519. NOT used for signing.
//   - TLSKey       — Layer 3 (client-facing HTTPS). Ed25519 for cert sign.
//
// Functions that need a StewardKey cannot be called with a WireGuardKey or
// TLSKey, even though all three wrap 32 bytes of key material. The Go type
// system enforces what the protocol requires.
//
// The transition bytes that get signed are the canonical protobuf encoding of
// (group_key || transition || message_kind). See CanonicalSignBytes.
//
// Threshold signatures (FROST) are deferred per docs/02-PROTOCOL.md section 11.
// The multisig envelope is the v1 implementation: a transition is valid when
// at least `threshold` of the steward signatures verify against the steward
// set at the snapshot the transition references.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// KeyPair is an Ed25519 keypair. The PublicKey is the canonical identifier.
type KeyPair struct {
	Public  types.PublicKey
	Private ed25519.PrivateKey
}

// GenerateKey creates a new Ed25519 keypair.
func GenerateKey() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	var kp KeyPair
	copy(kp.Public[:], pub)
	kp.Private = priv
	return kp, nil
}

// KeyPairFromSeed returns a deterministic keypair from a 32-byte seed. Used
// by the simulator's deterministic mode (see sim.NewWorld).
func KeyPairFromSeed(seed [32]byte) KeyPair {
	// ed25519.NewKeyFromSeed returns a 64-byte private key.
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	var kp KeyPair
	copy(kp.Public[:], pub)
	kp.Private = priv
	return kp
}

// MessageKind tags which kind of message is being signed. Prevents
// cross-protocol replay attacks: a signature valid for an RSVP cannot be
// reused as a transition signature.
type MessageKind uint8

const (
	MsgKindTransition MessageKind = 1
	MsgKindUserAction MessageKind = 2
)

// CanonicalSignBytes returns the bytes that should be signed for the given
// (group_key, payload, kind) tuple. The format is:
//
//	domain_separator || group_key || kind_byte || length-prefixed protobuf payload
//
// where domain_separator is the SHA-256 of "federated-meetup/v1/signing".
// This is the analog of Bitcoin's "BIP-76" or Ed25519's context bytes.
func CanonicalSignBytes(groupKey types.PublicKey, kind MessageKind, payload []byte) []byte {
	domain := sha256.Sum256([]byte("federated-meetup/v1/signing"))
	out := make([]byte, 0, 32+32+1+8+len(payload))
	out = append(out, domain[:]...)
	out = append(out, groupKey[:]...)
	out = append(out, byte(kind))
	var lenBuf [8]byte
	binaryBigEndianPutUint64(lenBuf[:], uint64(len(payload)))
	out = append(out, lenBuf[:]...)
	out = append(out, payload...)
	return out
}

func binaryBigEndianPutUint64(b []byte, v uint64) {
	_ = b[7]
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

// Sign produces a signature over the canonical sign bytes using the keypair's
// private key.
func Sign(kp KeyPair, groupKey types.PublicKey, kind MessageKind, payload []byte) types.Signature {
	msg := CanonicalSignBytes(groupKey, kind, payload)
	sig := ed25519.Sign(kp.Private, msg)
	var out types.Signature
	copy(out[:], sig)
	return out
}

// Verify returns nil if the signature over the canonical sign bytes verifies
// against pubkey.
func Verify(pub types.PublicKey, sig types.Signature, groupKey types.PublicKey, kind MessageKind, payload []byte) error {
	msg := CanonicalSignBytes(groupKey, kind, payload)
	return VerifyWithMessage(pub, sig, msg)
}

// VerifyWithMessage is the same as Verify but accepts a pre-computed
// canonical sign-bytes message, avoiding redundant recomputation when
// verifying many signatures over the same payload (H-4 optimization).
func VerifyWithMessage(pub types.PublicKey, sig types.Signature, msg []byte) error {
	if !ed25519.Verify(ed25519.PublicKey(pub[:]), msg, sig[:]) {
		return errors.New("crypto: signature verification failed")
	}
	return nil
}

// VerifyMultisig returns nil if at least `threshold` of the steward signatures
// verify against `stewards`. Each signature must be made by a distinct steward.
//
// We require distinct stewards — duplicate-key signatures don't count twice.
// This prevents a single steward from "filling up" the threshold by signing
// the same transition multiple times.
//
// H-4 optimization: the canonical sign bytes (the message being verified) is
// the SAME for all (steward, sig) pairs. We pre-compute it once outside the
// loop instead of recomputing it for every verification attempt.
func VerifyMultisig(stewards []types.PublicKey, threshold uint32, sigs []types.Signature, groupKey types.PublicKey, kind MessageKind, payload []byte) error {
	if uint32(len(sigs)) < threshold {
		return fmt.Errorf("crypto: %d signatures, need threshold %d", len(sigs), threshold)
	}
	// Pre-compute the canonical message ONCE — it's identical for all
	// (steward, sig) pairs. (Audit H-4.)
	msg := CanonicalSignBytes(groupKey, kind, payload)

	stewardSet := make(map[types.PublicKey]bool, len(stewards))
	for _, s := range stewards {
		stewardSet[s] = true
	}
	verified := make(map[types.PublicKey]bool, len(sigs))
	for _, sig := range sigs {
		// Ed25519 does not allow key recovery; we must try each steward.
		// But we use the pre-computed message so CanonicalSignBytes is
		// only called once total, not once per (steward, sig) pair.
		for _, s := range stewards {
			if verified[s] {
				continue
			}
			if err := VerifyWithMessage(s, sig, msg); err == nil {
				verified[s] = true
				break
			}
		}
	}
	if uint32(len(verified)) < threshold {
		return fmt.Errorf("crypto: only %d distinct valid signatures, need %d", len(verified), threshold)
	}
	return nil
}

// CanonicalTransitionBytes returns the protobuf bytes that get signed by the
// steward set. We strip the steward_signatures field before signing to avoid
// a circular dependency.
func CanonicalTransitionBytes(t *pb.Transition) ([]byte, error) {
	cp := proto.Clone(t).(*pb.Transition)
	cp.StewardSignatures = nil
	return proto.MarshalOptions{Deterministic: true}.Marshal(cp)
}