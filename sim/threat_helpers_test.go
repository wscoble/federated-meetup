// SPDX-License-Identifier: MIT
//
// Tiny helpers used by the threat-model tests. Kept in their own file
// to keep threat_test.go readable.

package sim_test

import (
	"crypto/sha256"

	"github.com/sscoble/federated-meetup/internal/types"
)

// sha256SumImpl is the canonical SHA-256 over arbitrary bytes.
func sha256SumImpl(b []byte) [32]byte {
	return sha256.Sum256(b)
}

// typesHashOfBytes builds a types.Hash from raw bytes (capped or padded
// to 32 bytes). Used to construct tx-hash-style identifiers in tests.
func typesHashOfBytes(b []byte) types.Hash {
	var out types.Hash
	copy(out[:], b)
	return out
}