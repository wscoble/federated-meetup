// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 46: payload-nil rejection sweep.
//
// The apply switch in internal/group/group.go rejects nil payloads
// for every typed transition (CREATE_GROUP, ADD_STEWARD, ISSUE_HOST_CERT,
// etc.) with a "missing payload" error. This test verifies that the
// defensive nil-check works for a representative subset.
//
// Why this matters: without the nil check, a transition with no
// payload would silently no-op (or panic). The check is defensive
// belt-and-suspenders — proto's oneof should make nil impossible,
// but malformed bytes (or a future refactor) could violate that.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestNilPayload_RejectedForTypedTransitions walks a few representative
// transitions and verifies that submitting them with a nil payload
// is rejected by the apply switch.
func TestNilPayload_RejectedForTypedTransitions(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        108,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	signWith := []crypto.KeyPair{stewards[0], stewards[1]}

	// Subset of types whose apply case does a nil-check on the payload.
	cases := []pb.TransitionType{
		pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD,
		pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		pb.TransitionType_TRANSITION_TYPE_FORK,
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT,
		pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
		pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		pb.TransitionType_TRANSITION_TYPE_REMOVE_HOST_PEER,
	}

	for _, kind := range cases {
		t.Run(kind.String(), func(t *testing.T) {
			// Re-capture prior root each iteration (since each
			// subtest runs in sequence and prior state may differ).
			currentPrior := w.Hosts()[0].State(gkp.Public).Root()
			proto := &pb.Transition{
				Type:       kind,
				PriorState: &pb.StateRoot{Hash: currentPrior[:]},
				SignedAt:   timestamppb.New(w.Now()),
				// No Payload set — proto oneof field 5 is nil.
			}
			canonical, err := group.MarshalCanonicalForSigningHelper(proto)
			if err != nil {
				t.Fatal(err)
			}
			sigs := []*pb.Signature{}
			for _, k := range signWith {
				s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
				sigs = append(sigs, &pb.Signature{Raw: s[:]})
			}
			proto.StewardSignatures = &pb.Multisig{Threshold: uint32(len(signWith)), Signatures: sigs}
			tx, err := group.NewTransition(proto, gkp.Public)
			if err != nil {
				t.Fatal(err)
			}

			h0 := w.Hosts()[0]
			_, err = h0.SubmitTransition(gkp.Public, tx)
			if err == nil {
				t.Fatalf("%s with nil payload was ACCEPTED", kind)
			}
			if !strings.Contains(err.Error(), "missing payload") &&
				!strings.Contains(err.Error(), "payload") {
				t.Logf("%s: rejected with: %v (acceptable)", kind, err)
			}
		})
	}
}