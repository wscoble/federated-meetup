// SPDX-License-Identifier: AGPL-3.0

package activitypub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// mockStore implements ProductStoreBackend for testing.
type mockStore struct {
	groups map[string]*pb.Group
	events map[string][]*pb.Event
}

func (m *mockStore) GetGroup(id string) (*pb.Group, bool) {
	g, ok := m.groups[id]
	return g, ok
}

func (m *mockStore) EventsForGroup(groupID string) []*pb.Event {
	return m.events[groupID]
}

func newMockStore() *mockStore {
	return &mockStore{
		groups: map[string]*pb.Group{
			"vegas-programmers": {
				GroupId:       "vegas-programmers",
				CanonicalName: "vegas-programmers",
				DisplayName:   "Vegas Programmers",
				Description:   "A community of developers in Las Vegas",
			},
		},
		events: map[string][]*pb.Event{
			"vegas-programmers": {
				{
					EventId:     "evt-go-night",
					GroupId:     "vegas-programmers",
					Title:       "Go Night: Building Federated Systems",
					Description: "A hands-on workshop",
					StartsAt:    timestamppb.Now(),
					Location:    "Innovation Center, Downtown Las Vegas",
				},
			},
		},
	}
}

func TestWebFinger(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=acct:vegas-programmers@fm.example.com", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if result["subject"] != "acct:vegas-programmers@fm.example.com" {
		t.Errorf("unexpected subject: %v", result["subject"])
	}

	links, ok := result["links"].([]interface{})
	if !ok || len(links) != 2 {
		t.Fatalf("expected 2 links, got %v", result["links"])
	}
}

func TestWebFingerGroupNotFound(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=acct:nonexistent@fm.example.com", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestActor(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/ap/actor/vegas-programmers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/activity+json" {
		t.Errorf("expected application/activity+json, got %s", ct)
	}

	var actor ActivityPubActor
	if err := json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
		t.Fatalf("failed to parse actor: %v", err)
	}

	if actor.Type != "Group" {
		t.Errorf("expected type Group, got %s", actor.Type)
	}
	if actor.PreferredUsername != "vegas-programmers" {
		t.Errorf("expected preferredUsername vegas-programmers, got %s", actor.PreferredUsername)
	}
	if actor.Name != "Vegas Programmers" {
		t.Errorf("expected name Vegas Programmers, got %s", actor.Name)
	}
}

func TestOutbox(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/ap/outbox/vegas-programmers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var collection OrderedCollection
	if err := json.Unmarshal(w.Body.Bytes(), &collection); err != nil {
		t.Fatalf("failed to parse collection: %v", err)
	}

	if collection.Type != "OrderedCollection" {
		t.Errorf("expected type OrderedCollection, got %s", collection.Type)
	}
	if collection.TotalItems != 1 {
		t.Fatalf("expected 1 item, got %d", collection.TotalItems)
	}

	// Verify the activity
	item, ok := collection.OrderedItems[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", collection.OrderedItems[0])
	}
	if item["type"] != "Create" {
		t.Errorf("expected Create activity, got %v", item["type"])
	}

	obj, ok := item["object"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object map, got %T", item["object"])
	}
	if obj["type"] != "Event" {
		t.Errorf("expected Event, got %v", obj["type"])
	}
	if obj["name"] != "Go Night: Building Federated Systems" {
		t.Errorf("unexpected event name: %v", obj["name"])
	}
}

func TestInboxFollow(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Send a Follow activity
	body := `{"type":"Follow","actor":"https://mastodon.social/users/alice","object":"https://fm.example.com/ap/actor/vegas-programmers"}`
	req := httptest.NewRequest("POST", "/ap/inbox/vegas-programmers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/activity+json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	followers := svc.Followers("vegas-programmers")
	if len(followers) != 1 {
		t.Fatalf("expected 1 follower, got %d", len(followers))
	}
	if followers[0] != "https://mastodon.social/users/alice" {
		t.Errorf("unexpected follower: %s", followers[0])
	}

	// Send an Undo Follow
	undoBody := `{"type":"Undo","actor":"https://mastodon.social/users/alice","object":{"type":"Follow","actor":"https://mastodon.social/users/alice","object":"https://fm.example.com/ap/actor/vegas-programmers"}}`
	req2 := httptest.NewRequest("POST", "/ap/inbox/vegas-programmers", strings.NewReader(undoBody))
	req2.Header.Set("Content-Type", "application/activity+json")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w2.Code)
	}

	followers = svc.Followers("vegas-programmers")
	if len(followers) != 0 {
		t.Fatalf("expected 0 followers after undo, got %d", len(followers))
	}
}

func TestInboxGet(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/ap/inbox/vegas-programmers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var collection OrderedCollection
	if err := json.Unmarshal(w.Body.Bytes(), &collection); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if collection.TotalItems != 0 {
		t.Errorf("expected 0 items, got %d", collection.TotalItems)
	}
}