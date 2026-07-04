// SPDX-License-Identifier: AGPL-3.0

package activitypub

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// SignatureParams holds the parsed components of an HTTP Signature header.
type SignatureParams struct {
	KeyID     string   // keyId — URL of the actor's public key
	Algorithm string   // algorithm — e.g. "rsa-sha256"
	Headers   []string // headers — list of signed headers
	Signature string   // signature — base64-encoded signature
}

// ParseSignatureHeader parses an HTTP Signature header value.
// Format: keyId="...",algorithm="...",headers="...",signature="..."
// Returns an error if the signature field is missing.
func ParseSignatureHeader(header string) (SignatureParams, error) {
	var params SignatureParams
	parts := strings.Split(header, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(kv[1], `"`)
		switch key {
		case "keyId":
			params.KeyID = val
		case "algorithm":
			params.Algorithm = val
		case "headers":
			params.Headers = strings.Fields(val)
		case "signature":
			params.Signature = val
		}
	}
	if params.Signature == "" {
		return SignatureParams{}, fmt.Errorf("signature field is required")
	}
	return params, nil
}

// VerifyHTTPSignature verifies an HTTP Signature on an incoming request.
// For v0: if no Signature header is present, returns nil (permissive mode).
// If a Signature header IS present, it parses the header but does not
// yet verify the cryptographic signature (the actor's public key fetch
// and verification will be added in a future cycle).
func VerifyHTTPSignature(r *http.Request, body []byte, client *http.Client) error {
	sigHeader := r.Header.Get("Signature")
	if sigHeader == "" {
		return nil // v0: accept unsigned requests
	}
	_, err := ParseSignatureHeader(sigHeader)
	if err != nil {
		return fmt.Errorf("invalid signature header: %w", err)
	}
	// TODO: fetch actor's public key via keyId, verify signature.
	// For now, accept all signed requests.
	return nil
}

// verifyDigest verifies a SHA-256 digest header value against a body.
// digest format: "SHA-256=<base64-encoded-sha256>"
func verifyDigest(digest string, body []byte) error {
	expected := "SHA-256=" + sha256Base64(body)
	if digest != expected {
		return fmt.Errorf("digest mismatch: expected %s, got %s", expected, digest)
	}
	return nil
}

// sha256Base64 returns the base64-encoded SHA-256 hash of the body.
func sha256Base64(body []byte) string {
	h := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(h[:])
}
