// SPDX-License-Identifier: MIT
//
// Verifier helpers for the new transition types (G1, G2, G3, G6, G8).
// Each function is called from State.Apply after the multisig envelope
// has been verified. They handle the type-specific business logic:
// the second-factor co-signature on ADD_HOST_PEER, the tier enum
// bounds on DECLARE_STEWARD_CUSTODY, the "slashed steward cannot
// co-sign" rule on SLASH_STEWARD, etc.
//
// The multisig envelope (M-of-N threshold over the steward set) is
// the universal gate. These helpers are the per-type gates layered
// on top.

package group

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// verifyAddHostPeerPayload validates the second-factor co-signature
// on an ADD_HOST_PEER transition. The steward threshold signature
// is verified earlier in Apply; this gate enforces the additional
// requirement that a CURRENT mesh member has co-signed the payload.
//
// Without this gate, the steward set alone could add any wg key to
// the mesh. With it, an existing peer must also vouch — so adding a
// peer requires steward consent AND a mesh member actively running
// the operation.
//
// Cycle 56 (audit fix C-1): the cosigner identity is the peer's
// dedicated Ed25519 CoSigner key (NOT the wg X25519 key, which is
// structurally incompatible with Ed25519 verification).
func verifyAddHostPeerPayload(s *State, p *pb.AddHostPeerPayload) error {
	// The cosigner's CoSigner (Ed25519) pubkey. This is the
	// identity under which the cosignature below is verified.
	cosignerKey := [32]byte{}
	copy(cosignerKey[:], p.GetCosignerPeerKey().GetRaw())
	if len(p.GetCosignerPeerKey().GetRaw()) != 32 {
		return errors.New("group: ADD_HOST_PEER rejected — cosigner_peer_key must be 32 bytes")
	}
	// The cosigner must be a current mesh member (verified via
	// CoSigner index, not wg key, cycle 56).
	if s.MeshPeerByCoSigner(cosignerKey) == nil {
		return fmt.Errorf("group: ADD_HOST_PEER rejected — co-signer %x is not a current mesh member", cosignerKey[:8])
	}
	// The cosignature is over the canonical AddHostPeerPayload
	// bytes EXCLUDING the CosignerPeerSignature field. This breaks
	// the chicken-and-egg: the test (or producer) signs the canonical
	// payload with placeholder bytes for the signature, then fills in
	// the real signature. Both sides agree because the placeholder
	// bytes are deterministic (zero-bytes) and the verifier strips the
	// field before marshaling.
	cp := proto.Clone(p).(*pb.AddHostPeerPayload)
	cp.CosignerPeerSignature = nil
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		return fmt.Errorf("group: marshal ADD_HOST_PEER for co-sig verify: %w", err)
	}
	if !verifyCoSignerSignature(cosignerKey[:], canonical, p.GetCosignerPeerSignature().GetRaw()) {
		return errors.New("group: ADD_HOST_PEER co-signature does not verify")
	}
	return nil
}

// verifyDeclareStewardCustody validates a custody declaration. The
// declaration is itself signed by the steward declaring their tier —
// the declaration cannot be forged by a third party because the
// steward envelope is required (it goes through the normal multisig
// path, with threshold signature from the steward set including the
// declaring steward). The per-payload gate enforces tier enum bounds.
func verifyDeclareStewardCustody(s *State, p *pb.DeclareStewardCustodyPayload) error {
	if p.GetTier() == pb.CustodyTier_CUSTODY_TIER_UNSPECIFIED {
		return errors.New("group: DECLARE_STEWARD_CUSTODY rejected — tier is UNSPECIFIED")
	}
	var key [32]byte
	copy(key[:], p.GetSteward().GetRaw())
	stewards := s.stewardsAtLocked(nil)
	for _, st := range stewards {
		if st.Key == key {
			return nil
		}
	}
	return fmt.Errorf("group: DECLARE_STEWARD_CUSTODY rejected — %x is not a current steward", key[:8])
}

// verifySlashStewardPayload validates the additional gate on
// SLASH_STEWARD: the slashed steward's key MUST NOT appear among the
// verifying multisig signers. The slashed steward cannot sign their
// own removal; the threshold of OTHER stewards must agree.
func verifySlashStewardPayload(s *State, p *pb.SlashStewardPayload, t *Transition) error {
	slashedKey := types.PublicKey{}
	copy(slashedKey[:], p.GetSlashedSteward().GetRaw())

	// The slashed steward MUST be a current steward. Without this
	// check, the slash is a no-op (and possibly an attack — trying
	// to remove a non-steward's reputation by falsely claiming they
	// were one).
	stewards := s.stewardsAtLocked(t.Proto.GetPriorState())
	isCurrent := false
	for _, st := range stewards {
		if st.Key == slashedKey {
			isCurrent = true
			break
		}
	}
	if !isCurrent {
		return fmt.Errorf("group: SLASH_STEWARD rejected — %x is not a current steward", slashedKey[:8])
	}

	// The slashed steward's signature MUST NOT be among the
	// verifying multisig signatures. Walk the envelope and verify
	// each signature; if the slashed steward's key verifies, reject.
	multisig := t.Proto.GetStewardSignatures()
	if multisig == nil {
		return errors.New("group: SLASH_STEWARD rejected — no signatures in envelope")
	}
	for _, sig := range multisig.GetSignatures() {
		var raw types.Signature
		copy(raw[:], sig.GetRaw())
		if err := verifyOne(slashedKey, raw, t.groupID, t.canonical); err == nil {
			return fmt.Errorf("group: SLASH_STEWARD rejected — slashed steward %x signed their own removal", slashedKey[:8])
		}
	}
	return nil
}

// verifyNameBindPayload validates the directory binding. The
// threshold of stewards have signed (the normal multisig path), so
// the gate here is structural: the name must be non-empty and
// not_after must be set.
func verifyNameBindPayload(s *State, p *pb.NameBindPayload) error {
	if err := validateStringField("name", p.GetName(), 1, 256); err != nil {
		return err
	}
	if p.GetNotAfter() == nil {
		return errors.New("group: NAME_BIND rejected — not_after is required")
	}
	return nil
}

// =============================================================================
// Storage keys for new transition types
// =============================================================================

func hostCertStorageKey(p *pb.IssueHostCertPayload) string {
	return fmt.Sprintf("host_cert/%s/%x/%d",
		p.GetHostname(),
		p.GetHostTlsKey().GetRaw(),
		p.GetNotAfter().GetSeconds(),
	)
}

func hostCertRevocationKey(p *pb.RevokeHostCertPayload) string {
	return fmt.Sprintf("host_cert_revoke/%s/%x/%d",
		p.GetHostname(),
		p.GetHostTlsKey().GetRaw(),
		p.GetNotAfter().GetSeconds(),
	)
}

// hostCertStorageKeyFromRevoke reconstructs the cert's storage key
// from a revocation payload. Used to tombstone the cert entry.
func hostCertStorageKeyFromRevoke(p *pb.RevokeHostCertPayload) string {
	return fmt.Sprintf("host_cert/%s/%x/%d",
		p.GetHostname(),
		p.GetHostTlsKey().GetRaw(),
		p.GetNotAfter().GetSeconds(),
	)
}

func nameBindStorageKey(p *pb.NameBindPayload) string {
	dir := p.GetDirectoryHost()
	if dir == "" {
		dir = "*"
	}
	return fmt.Sprintf("name_bind/%s/%s/%d", dir, p.GetName(), p.GetNotAfter().GetSeconds())
}

// =============================================================================
// Cryptographic helpers used by the gates
// =============================================================================

// verifyCoSignerSignature verifies an Ed25519 signature against the
// cosigner's dedicated Ed25519 CoSigner public key. This is NOT the
// WireGuard X25519 key — X25519 public keys are random 32-byte strings
// that are valid Ed25519 points only ~50% of the time, so using them
// for Ed25519 verification would silently reject ~half of legitimate
// cosignatures. The CoSigner key is a separate Ed25519 keypair generated
// at host-install time (see crypto.CoSignerKey / HostIdentity.CoSigner).
//
// The domain-separation prefix prevents the co-signature from being
// reused as a steward signature or any other kind of signature.
func verifyCoSignerSignature(cosignerPubKey, msg, sig []byte) bool {
	if len(cosignerPubKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	prefixed := domainPrefix("add_host_peer_cosig", msg)
	return ed25519.Verify(ed25519.PublicKey(cosignerPubKey), prefixed, sig)
}

// domainPrefix computes a deterministic, length-prefixed domain
// separator over the message. Used to prevent signature reuse across
// different message kinds.
func domainPrefix(domain string, msg []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(msg)))
	h.Write(lenBuf[:])
	h.Write(msg)
	return h.Sum(nil)
}

// signWithEd25519 is a helper used by tests and by callers that
// need to produce an Ed25519 signature with a domain prefix. It
// works with any Ed25519 private key (CoSigner, TLS, Steward).
// Not used in production code paths (production hosts produce
// signatures themselves) — included so tests can construct valid
// signed transitions without going through a full key generation flow.
//lint:ignore U1000 test helper kept for future use
func signWithEd25519(priv ed25519.PrivateKey, domain string, msg []byte) []byte {
	return ed25519.Sign(priv, domainPrefix(domain, msg))
}

// signStewardEd25519 is kept for backward compatibility with existing
// test code that references it by name.
//lint:ignore U1000 test helper kept for future use
func signStewardEd25519(priv ed25519.PrivateKey, domain string, msg []byte) []byte {
	return signWithEd25519(priv, domain, msg)
}

// avoid "imported and not used" for crypto in case future edits
// remove direct usage; this line keeps the import alive and
// documents intent.
var _ = crypto.MsgKindTransition