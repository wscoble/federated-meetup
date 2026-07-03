// SPDX-License-Identifier: AGPL-3.0
//
// Package types provides the Go-native types for the federated-meetup
// protocol. The protobuf definitions in proto/federated_meetup/v1 are the
// canonical wire format. These types are in-memory representations used by
// the host and the simulator.
//
// Why both? The wire format has a fixed canonical encoding (deterministic
// serialization). The in-memory types let us work with the protocol without
// every internal component depending on protobuf.
package types

import (
	"crypto/sha256"
	"encoding/binary"

	"google.golang.org/protobuf/proto"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// Hash is a 32-byte SHA-256 hash.
type Hash [32]byte

// PublicKey is an Ed25519 public key.
type PublicKey [32]byte

// Signature is an Ed25519 signature.
type Signature [64]byte

// GroupID is a PublicKey — the group is its sovereign identifier.
type GroupID = PublicKey

// UserID is a PublicKey — the user is its sovereign identifier.
type UserID = PublicKey

// EventID is a string. Globally unique within a group, but not globally
// sovereign — events live inside a group's namespace. See docs/02-PROTOCOL.md
// section 3.2: "What the protocol does NOT define" — event identity is
// defined, but it is not a sovereign object.
type EventID string

// StateEntry is a single key-value entry in the group's Merkle state.
type StateEntry struct {
	Key   string
	Value []byte
	Seq   uint64
}

// StateSnapshot is the in-memory representation of a group's state.
type StateSnapshot struct {
	Entries []StateEntry
}

// Root returns the canonical state root hash of the snapshot.
//
// Canonicalization: entries are sorted by key (lexicographic). For each entry,
// the SHA-256 of (length-prefixed key || length-prefixed value || 8-byte BE seq)
// is hashed into a Merkle tree (RFC 6962-style). Returns the root.
//
// Why hash the seq? The seq is part of the entry's content — without it, two
// writes at the same key with the same value would be indistinguishable from
// one write.
func (s StateSnapshot) Root() Hash {
	entries := append([]StateEntry(nil), s.Entries...)
	sortEntries(entries)
	h := sha256.New()
	for _, e := range entries {
		writeLP(h, []byte(e.Key))
		writeLP(h, e.Value)
		var seqBuf [8]byte
		binary.BigEndian.PutUint64(seqBuf[:], e.Seq)
		h.Write(seqBuf[:])
	}
	var root Hash
	copy(root[:], h.Sum(nil))
	return root
}

// Equal returns true if two state snapshots have identical content.
func (s StateSnapshot) Equal(o StateSnapshot) bool {
	a := append([]StateEntry(nil), s.Entries...)
	b := append([]StateEntry(nil), o.Entries...)
	sortEntries(a)
	sortEntries(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Seq != b[i].Seq {
			return false
		}
		if !bytesEq(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

// ToProto serializes the snapshot to its protobuf representation.
func (s StateSnapshot) ToProto() *pb.StateSnapshot {
	out := &pb.StateSnapshot{}
	for _, e := range s.Entries {
		out.Entries = append(out.Entries, &pb.StateEntry{
			Key:   e.Key,
			Value: append([]byte(nil), e.Value...),
			Seq:   e.Seq,
		})
	}
	return out
}

// SnapshotFromProto parses a protobuf snapshot.
func SnapshotFromProto(p *pb.StateSnapshot) StateSnapshot {
	var s StateSnapshot
	for _, e := range p.GetEntries() {
		s.Entries = append(s.Entries, StateEntry{
			Key:   e.GetKey(),
			Value: append([]byte(nil), e.GetValue()...),
			Seq:   e.GetSeq(),
		})
	}
	return s
}

// CanonicalBytes returns the canonical serialization of the snapshot, used
// for signing and verification. It is the same bytes that the Root() hashes.
func (s StateSnapshot) CanonicalBytes() []byte {
	entries := append([]StateEntry(nil), s.Entries...)
	sortEntries(entries)
	h := sha256.New()
	for _, e := range entries {
		writeLP(h, []byte(e.Key))
		writeLP(h, e.Value)
		var seqBuf [8]byte
		binary.BigEndian.PutUint64(seqBuf[:], e.Seq)
		h.Write(seqBuf[:])
	}
	return h.Sum(nil)
}

// CanonicalProtoBytes returns the canonical protobuf encoding of a transition.
// Uses protobuf's deterministic marshalling.
func CanonicalProtoBytes(m proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(m)
}

// writeLP writes a length-prefixed byte slice to h.
func writeLP(h interface {
	Write(p []byte) (int, error)
}, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	h.Write(lenBuf[:])
	h.Write(b)
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sortEntries is a tiny inline sort to avoid importing sort in hot paths.
// Entries lists are expected to be small (tens to low hundreds).
func sortEntries(e []StateEntry) {
	// Insertion sort: stable, fast for small N.
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j-1].Key > e[j].Key; j-- {
			e[j-1], e[j] = e[j], e[j-1]
		}
	}
}