// SPDX-License-Identifier: MIT
//
// Test helpers shared by sim_test.go and ddil_test.go. In a real _test.go
// file in the sim package, helpers used across tests live here.
package sim_test

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// setupVegasProgrammers creates a 3-steward, threshold-2 group with the
// "vegas-programmers" name on every host in the world. Returns the group's
// keypair (its PublicKey is the GroupID).
func setupVegasProgrammers(w *sim.World) crypto.KeyPair {
	stewardSeeds := []uint64{
		w.DeriveSeed("alice"),
		w.DeriveSeed("bob"),
		w.DeriveSeed("carol"),
	}
	stewards := make([]crypto.KeyPair, 3)
	for i, s := range stewardSeeds {
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		stewards[i] = crypto.KeyPairFromSeed(seed)
	}

	var groupSeed [32]byte
	gid := w.DeriveSeed("vegas-programmers")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	payload := &pb.CreateGroupPayload{
		CanonicalName:   "vegas-programmers",
		DisplayName:     "Vegas Programmers",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState: nil,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: payload},
		SignedAt:   timestamppb.New(w.Now()),
	})
	if err != nil {
		panic(err)
	}
	multisig := &pb.Multisig{
		Threshold: 2,
		Signatures: sigsFor(stewards[:2], groupKP.Public, canonical),
	}
	tr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState:        nil,
		Payload:           &pb.Transition_CreateGroup{CreateGroup: payload},
		SignedAt:          timestamppb.New(w.Now()),
		StewardSignatures: multisig,
	}, groupKP.Public)
	if err != nil {
		panic(err)
	}

	for _, h := range w.Hosts() {
		h.AddGroup(groupKP.Public, tr)
		if _, err := h.SubmitTransition(groupKP.Public, tr); err != nil {
			panic(err)
		}
	}
	return groupKP
}

// stewardPBs converts a slice of crypto.KeyPairs to the protobuf wire form.
func stewardPBs(stewards []crypto.KeyPair) []*pb.PublicKey {
	out := make([]*pb.PublicKey, len(stewards))
	for i, s := range stewards {
		p := s.Public
		out[i] = &pb.PublicKey{Raw: p[:]}
	}
	return out
}

// sigsFor signs `canonical` with each of the given keys, producing a slice of
// Signatures. The signatures verify against groupKey as the group identity.
func sigsFor(ks []crypto.KeyPair, groupKey types.PublicKey, canonical []byte) []*pb.Signature {
	out := make([]*pb.Signature, len(ks))
	for i, k := range ks {
		s := crypto.Sign(k, groupKey, crypto.MsgKindTransition, canonical)
		out[i] = &pb.Signature{Raw: s[:]}
	}
	return out
}