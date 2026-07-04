// SPDX-License-Identifier: AGPL-3.0
//
// Tiny helpers used by the threat-model tests. Kept in their own file
// to keep threat_test.go readable.

package sim_test

import (
	"crypto/sha256"

	"github.com/wscoble/federated-meetup/internal/types"
)

// sha256SumImpl is the canonical SHA-256 over arbitrary bytes.
func sha256SumImpl(b []byte) [32]byte {
	return sha256.Sum256(b)
}

// typesHashOfBytes builds a types.Hash from raw bytes (capped or padded
// to 32 bytes). Used to construct tx-hash-style identifiers in tests.
//lint:ignore U1000 test helper kept for future use
func typesHashOfBytes(b []byte) types.Hash {
	var out types.Hash
	copy(out[:], b)
	return out
}