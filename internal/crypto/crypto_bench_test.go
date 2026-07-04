// SPDX-License-Identifier: AGPL-3.0
//
// Benchmark for VerifyMultisig (audit H-4).
//
// Measures the latency of threshold-multisig verification with 100
// stewards and threshold 100 — the worst case identified in the audit.

package crypto

import (
	"testing"

	"github.com/wscoble/federated-meetup/internal/types"
)

// BenchmarkVerifyMultisig_100x100 measures the cost of verifying a
// 100-steward, threshold-100 multisig envelope. Before H-4, each
// (steward, sig) pair recomputed CanonicalSignBytes; after H-4 it is
// computed once. The benchmark verifies the optimization holds.
func BenchmarkVerifyMultisig_100x100(b *testing.B) {
	// Generate 100 steward keypairs.
	stewardKeys := make([]KeyPair, 100)
	stewardPubs := make([]types.PublicKey, 100)
	for i := range stewardKeys {
		var seed [32]byte
		seed[0] = byte(i)
		seed[1] = byte(i >> 8)
		stewardKeys[i] = KeyPairFromSeed(seed)
		stewardPubs[i] = stewardKeys[i].Public
	}

	groupKey := types.PublicKey{}
	groupKey[0] = 0xAB

	// Create a payload (canonical bytes will be computed inside VerifyMultisig).
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}

	// Produce 100 signatures (one per steward).
	sigs := make([]types.Signature, 100)
	for i, kp := range stewardKeys {
		sigs[i] = Sign(kp, groupKey, MsgKindTransition, payload)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := VerifyMultisig(stewardPubs, 100, sigs, groupKey, MsgKindTransition, payload); err != nil {
			b.Fatalf("VerifyMultisig failed: %v", err)
		}
	}
}