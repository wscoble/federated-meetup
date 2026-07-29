// SPDX-License-Identifier: AGPL-3.0
package push

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGenerateVAPIDKeys(t *testing.T) {
	keys, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}
	if keys.PublicKey == "" {
		t.Fatal("public key is empty")
	}
	if keys.PrivateKey == "" {
		t.Fatal("private key is empty")
	}

	// Generate a second keypair — should be different.
	keys2, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys (2): %v", err)
	}
	if keys.PublicKey == keys2.PublicKey {
		t.Fatal("two generated keypairs should differ")
	}
}

func TestNewManagerCreatesTables(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mgr, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// VAPID keys should be populated.
	if mgr.PublicKey() == "" {
		t.Fatal("PublicKey() is empty after init")
	}

	// Tables should exist.
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM vapid_keys`).Scan(&count)
	if err != nil {
		t.Fatalf("query vapid_keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("vapid_keys count = %d, want 1", count)
	}
}

func TestNewManagerLoadsExistingKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mgr1, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager (1): %v", err)
	}
	pub1 := mgr1.PublicKey()

	// Second Manager should load the same keys.
	mgr2, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager (2): %v", err)
	}
	pub2 := mgr2.PublicKey()

	if pub1 != pub2 {
		t.Fatalf("loaded key differs: %s != %s", pub1, pub2)
	}
}

func TestStoreAndGetSubscription(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mgr, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.StoreSubscription("alice@example.com", "https://push.example.com/sub/123", "p256dh-key", "auth-key", 1700000000)
	if err != nil {
		t.Fatalf("StoreSubscription: %v", err)
	}

	subs, err := mgr.GetSubscriptionsForUser("alice@example.com")
	if err != nil {
		t.Fatalf("GetSubscriptionsForUser: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}
	if subs[0].Endpoint != "https://push.example.com/sub/123" {
		t.Fatalf("endpoint = %s", subs[0].Endpoint)
	}
	if subs[0].P256dh != "p256dh-key" {
		t.Fatalf("p256dh = %s", subs[0].P256dh)
	}
	if subs[0].Auth != "auth-key" {
		t.Fatalf("auth = %s", subs[0].Auth)
	}
}

func TestStoreSubscriptionUpsert(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mgr, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Store for user1.
	_ = mgr.StoreSubscription("alice@example.com", "https://push.example.com/sub/123", "key1", "auth1", 1700000000)

	// Upsert same endpoint for user2.
	_ = mgr.StoreSubscription("bob@example.com", "https://push.example.com/sub/123", "key2", "auth2", 1700000001)

	subs, _ := mgr.GetSubscriptionsForUser("alice@example.com")
	if len(subs) != 0 {
		t.Fatalf("alice should have 0 subs after upsert, got %d", len(subs))
	}
	subs, _ = mgr.GetSubscriptionsForUser("bob@example.com")
	if len(subs) != 1 {
		t.Fatalf("bob should have 1 sub, got %d", len(subs))
	}
	if subs[0].P256dh != "key2" {
		t.Fatalf("p256dh = %s, want key2", subs[0].P256dh)
	}
}

func TestDeleteSubscription(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mgr, err := NewManager(db, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_ = mgr.StoreSubscription("alice@example.com", "https://push.example.com/sub/123", "key1", "auth1", 1700000000)

	if err := mgr.DeleteSubscription("https://push.example.com/sub/123"); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}

	subs, _ := mgr.GetSubscriptionsForUser("alice@example.com")
	if len(subs) != 0 {
		t.Fatalf("after delete, subs = %d, want 0", len(subs))
	}
}