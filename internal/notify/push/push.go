// SPDX-License-Identifier: AGPL-3.0
//
// Package push implements browser push notifications using the Web Push
// Protocol (VAPID). It generates a VAPID keypair on first run, stores it
// in SQLite, and sends push messages via the webpush-go library.
package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/SherClockHolmes/webpush-go"
)

// VAPIDKeys holds the public/private keypair for VAPID authentication.
type VAPIDKeys struct {
	PublicKey  string // base64url-encoded (P-256 uncompressed)
	PrivateKey string // base64url-encoded
}

// Manager handles VAPID key management and push message delivery.
type Manager struct {
	db   *sql.DB
	keys VAPIDKeys
	subject string // mailto: URL for VAPID
}

// NewManager creates a push Manager. It loads or generates VAPID keys
// from the given SQLite database. subject is the VAPID subject
// (typically "mailto:admin@example.com").
func NewManager(db *sql.DB, subject string) (*Manager, error) {
	if db == nil {
		return nil, fmt.Errorf("push: db is nil")
	}
	// Ensure the vapid_keys table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vapid_keys (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		public_key TEXT NOT NULL,
		private_key TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("push: create vapid_keys table: %w", err)
	}

	// Also ensure the push_subscriptions table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS push_subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_email TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		p256dh TEXT NOT NULL DEFAULT '',
		auth TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		UNIQUE(endpoint)
	)`); err != nil {
		return nil, fmt.Errorf("push: create push_subscriptions table: %w", err)
	}

	m := &Manager{
		db:      db,
		subject: subject,
	}

	// Load or generate keys.
	if err := m.loadOrGenerateKeys(); err != nil {
		return nil, err
	}

	return m, nil
}

// loadOrGenerateKeys loads VAPID keys from SQLite, or generates a new
// keypair and stores it if none exists.
func (m *Manager) loadOrGenerateKeys() error {
	var pub, priv string
	err := m.db.QueryRow(`SELECT public_key, private_key FROM vapid_keys WHERE id = 1`).Scan(&pub, &priv)
	if err == nil {
		m.keys = VAPIDKeys{PublicKey: pub, PrivateKey: priv}
		log.Printf("push: loaded VAPID keys from DB (public=%s...)", pub[:16])
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("push: query vapid keys: %w", err)
	}

	// Generate new keypair.
	keys, err := GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("push: generate vapid keys: %w", err)
	}
	m.keys = keys

	// Store in DB.
	_, err = m.db.Exec(
		`INSERT INTO vapid_keys (id, public_key, private_key) VALUES (1, ?, ?)`,
		keys.PublicKey, keys.PrivateKey,
	)
	if err != nil {
		return fmt.Errorf("push: store vapid keys: %w", err)
	}
	log.Printf("push: generated and stored new VAPID keys (public=%s...)", keys.PublicKey[:16])
	return nil
}

// PublicKey returns the VAPID public key (base64url-encoded).
func (m *Manager) PublicKey() string {
	return m.keys.PublicKey
}

// Keys returns the full VAPID keypair.
func (m *Manager) Keys() VAPIDKeys {
	return m.keys
}

// Send pushes a notification to a web push subscription. The
// subscriptionJSON must be a valid Web Push API PushSubscription.toJSON()
// object containing endpoint, keys.p256dh, and keys.auth.
func (m *Manager) Send(ctx context.Context, subscriptionJSON, title, body string) error {
	if subscriptionJSON == "" {
		return fmt.Errorf("push: empty subscription")
	}

	var sub webpush.Subscription
	if err := json.Unmarshal([]byte(subscriptionJSON), &sub); err != nil {
		return fmt.Errorf("push: parse subscription: %w", err)
	}

	// Build the notification payload.
	payload := fmt.Sprintf(`{"title":%q,"body":%q}`, title, body)

	resp, err := webpush.SendNotificationWithContext(ctx, []byte(payload), &sub, &webpush.Options{
		Subscriber:      m.subject,
		VAPIDPublicKey:  m.keys.PublicKey,
		VAPIDPrivateKey: m.keys.PrivateKey,
		TTL:             24 * 60 * 60, // 24 hours
	})
	if err != nil {
		return fmt.Errorf("push: send notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("push: server returned %d", resp.StatusCode)
	}
	return nil
}

// ---- Push subscription storage ----

// Subscription holds a web push subscription for a user.
type Subscription struct {
	ID        int64
	UserEmail string
	Endpoint  string
	P256dh    string
	Auth      string
	CreatedAt int64
}

// StoreSubscription stores a push subscription for a user. If the
// endpoint already exists, it updates the associated user and keys.
func (m *Manager) StoreSubscription(userEmail, endpoint, p256dh, auth string, createdAt int64) error {
	_, err := m.db.Exec(
		`INSERT INTO push_subscriptions (user_email, endpoint, p256dh, auth, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		   user_email=excluded.user_email,
		   p256dh=excluded.p256dh,
		   auth=excluded.auth`,
		userEmail, endpoint, p256dh, auth, createdAt,
	)
	if err != nil {
		return fmt.Errorf("push: store subscription: %w", err)
	}
	return nil
}

// GetSubscriptionsForUser returns all push subscriptions for a user.
func (m *Manager) GetSubscriptionsForUser(userEmail string) ([]Subscription, error) {
	rows, err := m.db.Query(
		`SELECT id, user_email, endpoint, p256dh, auth, created_at FROM push_subscriptions WHERE user_email = ?`,
		userEmail,
	)
	if err != nil {
		return nil, fmt.Errorf("push: query subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.UserEmail, &s.Endpoint, &s.P256dh, &s.Auth, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSubscription removes a subscription by endpoint.
func (m *Manager) DeleteSubscription(endpoint string) error {
	_, err := m.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// ---- Key generation ----

// GenerateVAPIDKeys generates a new P-256 ECDSA keypair and returns
// the keys as base64url-encoded strings suitable for VAPID.
func GenerateVAPIDKeys() (VAPIDKeys, error) {
	// Generate a P-256 ECDSA key.
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return VAPIDKeys{}, fmt.Errorf("generate ECDSA key: %w", err)
	}

	// The webpush-go library expects the private key as a base64url-encoded
	// string of the raw 32-byte private scalar. The public key is the
	// base64url-encoded uncompressed point (65 bytes: 0x04 || X || Y).
	privBytes := privKey.D.FillBytes(make([]byte, 32))
	pubBytes := elliptic.MarshalCompressed(elliptic.P256(), privKey.PublicKey.X, privKey.PublicKey.Y)
	// webpush-go uses uncompressed format.
	pubUncompressed := append([]byte{0x04}, privKey.PublicKey.X.Bytes()...)
	pubUncompressed = append(pubUncompressed, privKey.PublicKey.Y.Bytes()...)
	// Pad to 32 bytes each
	xBytes := make([]byte, 32)
	yBytes := make([]byte, 32)
	copy(xBytes[32-len(privKey.PublicKey.X.Bytes()):], privKey.PublicKey.X.Bytes())
	copy(yBytes[32-len(privKey.PublicKey.Y.Bytes()):], privKey.PublicKey.Y.Bytes())
	pubUncompressed = append([]byte{0x04}, append(xBytes, yBytes...)...)
	_ = pubBytes // suppress unused

	return VAPIDKeys{
		PublicKey:  base64.RawURLEncoding.EncodeToString(pubUncompressed),
		PrivateKey: base64.RawURLEncoding.EncodeToString(privBytes),
	}, nil
}