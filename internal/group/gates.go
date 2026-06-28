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
func verifyAddHostPeerPayload(s *State, p *pb.AddHostPeerPayload) error {
	cosignerKey := [32]byte{}
	copy(cosignerKey[:], p.GetCosignerPeerKey().GetRaw())
	if !s.IsMeshMemberLocked(cosignerKey) {
		return fmt.Errorf("group: ADD_HOST_PEER rejected — co-signer %x is not a current mesh member", cosignerKey[:8])
	}
	// The cosigner signature is over the canonical AddHostPeerPayload
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
	// The co-signer's wg key is an X25519 key (used for wg handshakes),
	// not an Ed25519 key. We can't use it directly as an Ed25519
	// verifier. The wire format treats this co-signature as the wg
	// peer's signature using its identity key (the wg key IS the
	// identity key for the co-signing peer, signed via Ed25519 over
	// the same key bytes — the protocol requires hosts to also hold
	// an Ed25519 view of their wg key for this purpose).
	//
	// This is enforced at the host-operator level: when a host joins
	// the mesh, it presents BOTH its wg X25519 key AND an Ed25519
	// signature using the same 32 bytes interpreted as an Ed25519
	// pubkey. The Ed25519 view is the co-signer identity.
	if !verifyCoSignerSignature(p.GetCosignerPeerKey().GetRaw(), canonical, p.GetCosignerPeerSignature().GetRaw()) {
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
	if p.GetName() == "" {
		return errors.New("group: NAME_BIND rejected — name is empty")
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

// verifyCoSignerSignature verifies an Ed25519 signature where the
// 32-byte "public key" is actually a wg X25519 key being interpreted
// as an Ed25519 key. This is the protocol convention: when a host
// joins the mesh, it registers the same 32 bytes as both an X25519
// wg key and an Ed25519 signing identity.
//
// The domain-separation prefix prevents the co-signature from being
// reused as a steward signature or any other kind of signature.
func verifyCoSignerSignature(wgPubKey, msg, sig []byte) bool {
	if len(wgPubKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	prefixed := domainPrefix("add_host_peer_cosig", msg)
	return ed25519.Verify(ed25519.PublicKey(wgPubKey), prefixed, sig)
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

// signStewardEd25519 is a helper used by tests and by callers that
// need to produce a co-signature using the wg-key-as-ed25519-key
// convention. Not used in production code paths (production hosts
// produce signatures themselves) — included so tests can construct
// valid ADD_HOST_PEER transitions without going through wireguard-go.
func signStewardEd25519(priv ed25519.PrivateKey, domain string, msg []byte) []byte {
	return ed25519.Sign(priv, domainPrefix(domain, msg))
}

// avoid "imported and not used" for crypto in case future edits
// remove direct usage; this line keeps the import alive and
// documents intent.
var _ = crypto.MsgKindTransition