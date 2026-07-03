// SPDX-License-Identifier: AGPL-3.0
//
// Three independent key domains, per docs/02-PROTOCOL.md §5.1.
//
// These types wrap raw key material in named structs so the compiler rejects
// accidental cross-layer key reuse. The underlying bytes (32 bytes for all
// three curves) are deliberately not exported; consumers must use the typed
// accessor methods.
//
// Conversion rules:
//
//	StewardKey   <-> StewardKey   : yes, by definition
//	WireGuardKey <-> WireGuardKey : yes, by definition
//	TLSKey       <-> TLSKey       : yes, by definition
//
// Anything else is a compile error.

package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"

	"github.com/sscoble/federated-meetup/internal/types"
)

// =============================================================================
// Layer 2 — StewardKey (Ed25519 governance signing)
// =============================================================================

// StewardKey is an Ed25519 key used to sign state transitions. This is the
// governance key — the one M-of-N stewards hold shares of.
type StewardKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// StewardPublicKey is the public half of a StewardKey, suitable for inclusion
// in steward sets and for verifying signatures.
type StewardPublicKey struct {
	pub ed25519.PublicKey
}

// GenerateStewardKey creates a fresh StewardKey from the given reader.
func GenerateStewardKey(r io.Reader) (StewardKey, error) {
	if r == nil {
		r = rand.Reader
	}
	pub, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return StewardKey{}, fmt.Errorf("crypto: generate steward key: %w", err)
	}
	return StewardKey{pub: pub, priv: priv}, nil
}

// Public returns the public half (safe to serialize).
func (k StewardKey) Public() StewardPublicKey {
	return StewardPublicKey{pub: k.pub}
}

// Bytes returns the 32-byte canonical encoding of the public key.
func (k StewardPublicKey) Bytes() []byte { return k.pub }

// StewardPublicKeyFromBytes reconstructs a StewardPublicKey from canonical bytes.
func StewardPublicKeyFromBytes(b []byte) (StewardPublicKey, error) {
	if len(b) != ed25519.PublicKeySize {
		return StewardPublicKey{}, fmt.Errorf("crypto: steward public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return StewardPublicKey{pub: ed25519.PublicKey(b)}, nil
}

// Sign produces an Ed25519 signature over msg using the private key.
func (k StewardKey) Sign(msg []byte) []byte {
	return ed25519.Sign(k.priv, msg)
}

// Verify checks an Ed25519 signature with this public key.
func (k StewardPublicKey) Verify(msg, sig []byte) bool {
	return ed25519.Verify(k.pub, msg, sig)
}

// =============================================================================
// Layer 1 — WireGuardKey (X25519 mesh transport)
// =============================================================================

// WireGuardKey is an X25519 key used for WireGuard mesh handshakes. NOT for
// signing. A WireGuardKey can derive a shared secret (Noise IK) but cannot
// produce an Ed25519 signature.
type WireGuardKey struct {
	priv [32]byte
	pub  [32]byte
}

// WireGuardPublicKey is the public half of a WireGuardKey.
type WireGuardPublicKey struct {
	pub [32]byte
}

// GenerateWireGuardKey creates a fresh WireGuardKey.
func GenerateWireGuardKey() (WireGuardKey, error) {
	var k WireGuardKey
	if _, err := rand.Read(k.priv[:]); err != nil {
		return WireGuardKey{}, fmt.Errorf("crypto: generate wireguard key: %w", err)
	}
	pub, err := curve25519.X25519(k.priv[:], curve25519.Basepoint)
	if err != nil {
		return WireGuardKey{}, fmt.Errorf("crypto: derive wireguard pub: %w", err)
	}
	copy(k.pub[:], pub)
	return k, nil
}

// Public returns the public half.
func (k WireGuardKey) Public() WireGuardPublicKey {
	return WireGuardPublicKey{pub: k.pub}
}

// PrivateBytes returns the 32-byte private key. Used by tests that
// need to demonstrate the X25519/Ed25519 key derivation mismatch.
// Not used in production code paths.
func (k WireGuardKey) PrivateBytes() []byte {
	out := make([]byte, 32)
	copy(out, k.priv[:])
	return out
}

// Bytes returns the 32-byte public key (canonical wireguard encoding).
func (k WireGuardPublicKey) Bytes() []byte {
	out := make([]byte, 32)
	copy(out, k.pub[:])
	return out
}

// WireGuardPublicKeyFromBytes reconstructs a public key.
func WireGuardPublicKeyFromBytes(b []byte) (WireGuardPublicKey, error) {
	if len(b) != 32 {
		return WireGuardPublicKey{}, fmt.Errorf("crypto: wireguard public key must be 32 bytes, got %d", len(b))
	}
	var k WireGuardPublicKey
	copy(k.pub[:], b)
	return k, nil
}

// SharedSecret computes the X25519 ECDH result with a remote public key.
// This is what the Noise IK handshake uses to derive session keys.
// Returns an error on all-zero input (a known X25519 footgun).
func (k WireGuardKey) SharedSecret(remote WireGuardPublicKey) ([]byte, error) {
	out, err := curve25519.X25519(k.priv[:], remote.pub[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: x25519: %w", err)
	}
	// Reject all-zero output (low-order point attack).
	var zero [32]byte
	var outArr [32]byte
	copy(outArr[:], out)
	if outArr == zero {
		return nil, errors.New("crypto: x25519 produced all-zero output (low-order point)")
	}
	return out, nil
}

// =============================================================================
// Layer 3 — TLSKey (Ed25519 server certificate signing)
// =============================================================================

// TLSKey is an Ed25519 key used to sign the host's TLS certificate (or to
// pin to a CA's certificate). Distinct from StewardKey so a compromised
// TLS key does not let an attacker sign transitions.
type TLSKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// TLSPublicKey is the public half of a TLSKey.
type TLSPublicKey struct {
	pub ed25519.PublicKey
}

// GenerateTLSKey creates a fresh TLSKey.
func GenerateTLSKey(r io.Reader) (TLSKey, error) {
	if r == nil {
		r = rand.Reader
	}
	pub, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return TLSKey{}, fmt.Errorf("crypto: generate tls key: %w", err)
	}
	return TLSKey{pub: pub, priv: priv}, nil
}

// Public returns the public half.
func (k TLSKey) Public() TLSPublicKey {
	return TLSPublicKey{pub: k.pub}
}

// Bytes returns the 32-byte public key.
func (k TLSPublicKey) Bytes() []byte { return k.pub }

// TLSPublicKeyFromBytes reconstructs a public key.
func TLSPublicKeyFromBytes(b []byte) (TLSPublicKey, error) {
	if len(b) != ed25519.PublicKeySize {
		return TLSPublicKey{}, fmt.Errorf("crypto: tls public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return TLSPublicKey{pub: ed25519.PublicKey(b)}, nil
}

// =============================================================================
// Identity bundle — a host's complete key set
// =============================================================================

// CoSignerKey is an Ed25519 key used by a host to cosign ADD_HOST_PEER
// transitions. Distinct from StewardKey because the cosignature is on a
// different domain ("add_host_peer_cosig") and is verified against the
// host's mesh-membership identity rather than the steward set.
//
// Cycle 52 (audit fix C-1): previously the protocol required hosts to use
// their WireGuard X25519 key bytes as an Ed25519 pubkey for cosignature
// verification. Empirically, NO randomly-generated X25519 key is also a
// valid Ed25519 point (cycle 51: 0/256 coincide). The CoSignerKey is the
// correct fix: a separate Ed25519 keypair dedicated to cosignatures.
type CoSignerKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// CoSignerPublicKey is the public half of a CoSignerKey.
type CoSignerPublicKey struct {
	pub ed25519.PublicKey
}

// GenerateCoSignerKey creates a fresh CoSignerKey.
func GenerateCoSignerKey(r io.Reader) (CoSignerKey, error) {
	if r == nil {
		r = rand.Reader
	}
	pub, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return CoSignerKey{}, fmt.Errorf("crypto: generate cosigner key: %w", err)
	}
	return CoSignerKey{pub: pub, priv: priv}, nil
}

// Public returns the public half.
func (k CoSignerKey) Public() CoSignerPublicKey {
	return CoSignerPublicKey{pub: k.pub}
}

// PrivateKey returns the Ed25519 private key. Used by callers that
// need to produce cosignatures (e.g., the host daemon signing an
// ADD_HOST_PEER cosignature). Test helpers also use this.
func (k CoSignerKey) PrivateKey() ed25519.PrivateKey {
	return k.priv
}

// Bytes returns the 32-byte public key.
func (k CoSignerPublicKey) Bytes() []byte { return k.pub }

// CoSignerPublicKeyFromBytes reconstructs a public key.
func CoSignerPublicKeyFromBytes(b []byte) (CoSignerPublicKey, error) {
	if len(b) != ed25519.PublicKeySize {
		return CoSignerPublicKey{}, fmt.Errorf("crypto: cosigner public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return CoSignerPublicKey{pub: ed25519.PublicKey(b)}, nil
}

// HostIdentity is the bundle of independent key domains owned by one
// host. The host operator generates this at install time and stores it in a
// single secret file (or split across KMS-backed keyrings).
type HostIdentity struct {
	Steward   StewardKey       // signs transitions on behalf of THIS host
	WireGuard WireGuardKey     // authenticates this host on the mesh
	TLS       TLSKey           // signs this host's TLS cert
	CoSigner  CoSignerKey      // cosigns ADD_HOST_PEER transitions (cycle 52)
	HostID    types.PublicKey  // canonical identifier (== Steward.Public().Bytes())
}

// GenerateHostIdentity creates a fresh identity bundle.
func GenerateHostIdentity() (HostIdentity, error) {
	s, err := GenerateStewardKey(nil)
	if err != nil {
		return HostIdentity{}, err
	}
	w, err := GenerateWireGuardKey()
	if err != nil {
		return HostIdentity{}, err
	}
	t, err := GenerateTLSKey(nil)
	if err != nil {
		return HostIdentity{}, err
	}
	c, err := GenerateCoSignerKey(nil)
	if err != nil {
		return HostIdentity{}, err
	}
	return HostIdentity{
		Steward:   s,
		WireGuard: w,
		TLS:       t,
		CoSigner:  c,
		HostID:    types.PublicKey(s.Public().Bytes()),
	}, nil
}

// =============================================================================
// Fingerprint helper (logging-safe)
// =============================================================================

// Fingerprint returns a short, non-reversible identifier for a public key.
// Safe to log. Used in audit trails where leaking key material would be bad
// but knowing "this transition came from the same key as the last one" is
// useful.
func Fingerprint(pubBytes []byte) string {
	sum := sha256.Sum256(pubBytes)
	return fmt.Sprintf("%x", sum[:6])
}