// SPDX-License-Identifier: AGPL-3.0

package activitypub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// mockActorServer simulates a remote AP actor with an inbox.
// It serves:
//   - GET /actor → actor document with "inbox" field
//   - POST /inbox → receives delivered activities
type mockActorServer struct {
	receivedActivities []map[string]interface{}
	acceptCount        int32
	rejectCount        int32
	serveRejections    int32 // how many GET requests to reject (for testing retry)
	contentType        string
}

func newMockActorServer() *mockActorServer {
	return &mockActorServer{}
}

func (m *mockActorServer) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/actor":
		// Simulate serveRejections: reject first N requests
		if atomic.LoadInt32(&m.serveRejections) > 0 {
			atomic.AddInt32(&m.serveRejections, -1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/activity+json")
		fmt.Fprintf(w, `{"type":"Person","id":"%s","inbox":"%s/inbox","outbox":"%s/outbox"}`, r.Host+r.URL.Path, baseURL(r), baseURL(r))
	case r.Method == "POST" && r.URL.Path == "/inbox":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var activity map[string]interface{}
		if err := json.Unmarshal(body, &activity); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.receivedActivities = append(m.receivedActivities, activity)

		// Check content type
		m.contentType = r.Header.Get("Content-Type")

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted"}`))
		atomic.AddInt32(&m.acceptCount, 1)
	default:
		http.NotFound(w, r)
	}
}

// baseURL returns the base URL of the test server
func baseURL(r *http.Request) string {
	return fmt.Sprintf("http://%s", r.Host)
}

func TestDeliverActivity(t *testing.T) {
	// Start mock actor server
	mockActor := newMockActorServer()
	mockServer := httptest.NewServer(http.HandlerFunc(mockActor.handler))
	defer mockServer.Close()

	// Create AP service
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)
	svc.SetHTTPClient(mockServer.Client())

	// Add a follower (the mock actor)
	followerURL := mockServer.URL + "/actor"
	svc.followers["vegas-programmers"] = []string{followerURL}

	// Create an activity
	activity := &ActivityPubActivity{
		Context: "https://www.w3.org/ns/activitystreams",
		Type:    "Create",
		ID:      "https://fm.example.com/ap/actor/vegas-programmers/activities/create-evt-test",
		Actor:   "https://fm.example.com/ap/actor/vegas-programmers",
		Object: APEvent{
			Type:      "Event",
			ID:        "https://fm.example.com/events/vegas-programmers/evt-test",
			Name:      "Test Event",
			StartTime: "2026-07-04T19:00:00Z",
		},
		To: []string{"https://www.w3.org/ns/activitystreams#Public"},
	}

	// Deliver
	report, err := svc.DeliverActivity(t.Context(), activity, "vegas-programmers")
	if err != nil {
		t.Fatalf("DeliverActivity failed: %v", err)
	}

	if report.Successes != 1 {
		t.Errorf("expected 1 success, got %d (failures: %d, errors: %v)", report.Successes, report.Failures, report.Errors)
	}
	if report.Failures != 0 {
		t.Errorf("expected 0 failures, got %d: %v", report.Failures, report.Errors)
	}

	// Verify the mock received the activity
	if len(mockActor.receivedActivities) != 1 {
		t.Fatalf("expected 1 received activity, got %d", len(mockActor.receivedActivities))
	}
	recv := mockActor.receivedActivities[0]
	if recv["type"] != "Create" {
		t.Errorf("expected Create activity, got %v", recv["type"])
	}
	obj, ok := recv["object"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object map, got %T", recv["object"])
	}
	if obj["name"] != "Test Event" {
		t.Errorf("expected event name 'Test Event', got %v", obj["name"])
	}

	// Verify content type
	if !strings.Contains(mockActor.contentType, "application/activity+json") {
		t.Errorf("expected Content-Type application/activity+json, got %s", mockActor.contentType)
	}
}

func TestDeliverActivityNoFollowers(t *testing.T) {
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	activity := &ActivityPubActivity{
		Type:  "Create",
		ID:    "https://fm.example.com/activities/test",
		Actor: "https://fm.example.com/ap/actor/vegas-programmers",
	}

	report, err := svc.DeliverActivity(t.Context(), activity, "vegas-programmers")
	if err != nil {
		t.Fatalf("DeliverActivity failed: %v", err)
	}
	if report.Successes != 0 || report.Failures != 0 {
		t.Errorf("expected 0 successes and 0 failures, got %d/%d", report.Successes, report.Failures)
	}
}

func TestDeliverActivityFailure(t *testing.T) {
	// Start mock server that returns 404 for inbox
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/actor" {
			w.Header().Set("Content-Type", "application/activity+json")
			fmt.Fprintf(w, `{"inbox":"%s/inbox"}`, baseURL(r))
			return
		}
		// Inbox always returns 404
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)
	svc.SetHTTPClient(mockServer.Client())

	followerURL := mockServer.URL + "/actor"
	svc.followers["vegas-programmers"] = []string{followerURL}

	activity := &ActivityPubActivity{
		Type:  "Create",
		ID:    "https://fm.example.com/activities/test",
		Actor: "https://fm.example.com/ap/actor/vegas-programmers",
	}

	report, err := svc.DeliverActivity(t.Context(), activity, "vegas-programmers")
	if err != nil {
		t.Fatalf("DeliverActivity failed: %v", err)
	}
	if report.Successes != 0 {
		t.Errorf("expected 0 successes, got %d", report.Successes)
	}
	if report.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", report.Failures)
	}
}

func TestDeliverNewEvent(t *testing.T) {
	mockActor := newMockActorServer()
	mockServer := httptest.NewServer(http.HandlerFunc(mockActor.handler))
	defer mockServer.Close()

	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)
	svc.SetHTTPClient(mockServer.Client())

	followerURL := mockServer.URL + "/actor"
	svc.followers["vegas-programmers"] = []string{followerURL}

	event := &pb.Event{
		EventId:     "evt-deliver-test",
		GroupId:     "vegas-programmers",
		Title:       "Delivered Event",
		Description: "An event delivered via ActivityPub",
		StartsAt:    timestamppb.Now(),
		Location:    "Online",
	}

	report, err := svc.DeliverNewEvent(event, "vegas-programmers")
	if err != nil {
		t.Fatalf("DeliverNewEvent failed: %v", err)
	}
	if report.Successes != 1 {
		t.Errorf("expected 1 success, got %d (failures: %d, errors: %v)", report.Successes, report.Failures, report.Errors)
	}

	// Verify the delivered activity
	if len(mockActor.receivedActivities) != 1 {
		t.Fatalf("expected 1 received activity, got %d", len(mockActor.receivedActivities))
	}
	recv := mockActor.receivedActivities[0]
	if recv["type"] != "Create" {
		t.Errorf("expected Create, got %v", recv["type"])
	}
	obj, ok := recv["object"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object map, got %T", recv["object"])
	}
	if obj["name"] != "Delivered Event" {
		t.Errorf("expected 'Delivered Event', got %v", obj["name"])
	}
	if obj["type"] != "Event" {
		t.Errorf("expected Event, got %v", obj["type"])
	}
}

func TestDiscoverInboxURL(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/actor" {
			w.Header().Set("Content-Type", "application/activity+json")
			fmt.Fprintf(w, `{"inbox":"%s/inbox"}`, baseURL(r))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)
	svc.SetHTTPClient(mockServer.Client())

	inboxURL, err := svc.DiscoverInboxURL(t.Context(), mockServer.URL+"/actor")
	if err != nil {
		t.Fatalf("DiscoverInboxURL failed: %v", err)
	}
	if inboxURL != mockServer.URL+"/inbox" {
		t.Errorf("expected %s/inbox, got %s", mockServer.URL, inboxURL)
	}
}

func TestDiscoverInboxURLNoInboxField(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		fmt.Fprintf(w, `{"type":"Person","id":"%s/actor"}`, baseURL(r))
	}))
	defer mockServer.Close()

	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)
	svc.SetHTTPClient(mockServer.Client())

	_, err := svc.DiscoverInboxURL(t.Context(), mockServer.URL+"/actor")
	if err == nil {
		t.Fatal("expected error for missing inbox field")
	}
}

func TestInboxNoSignatureAccepted(t *testing.T) {
	// Verify that inbox accepts POST without Signature header (v0 behavior)
	store := newMockStore()
	adapter := NewProductStoreAdapter(store)
	svc := NewActivityPubService("https://fm.example.com", adapter)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	body := `{"type":"Follow","actor":"https://mastodon.social/users/bob","object":"https://fm.example.com/ap/actor/vegas-programmers"}`
	req := httptest.NewRequest("POST", "/ap/inbox/vegas-programmers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/activity+json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (accepted without signature), got %d: %s", w.Code, w.Body.String())
	}

	followers := svc.Followers("vegas-programmers")
	if len(followers) != 1 {
		t.Fatalf("expected 1 follower, got %d", len(followers))
	}
}

func TestParseSignatureHeader(t *testing.T) {
	header := `keyId="https://example.com/actor#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="base64sig=="`

	params, err := ParseSignatureHeader(header)
	if err != nil {
		t.Fatalf("ParseSignatureHeader failed: %v", err)
	}
	if params.KeyID != "https://example.com/actor#main-key" {
		t.Errorf("expected keyId, got %s", params.KeyID)
	}
	if params.Algorithm != "rsa-sha256" {
		t.Errorf("expected algorithm, got %s", params.Algorithm)
	}
	if params.Signature != "base64sig==" {
		t.Errorf("expected signature, got %s", params.Signature)
	}
	if len(params.Headers) != 4 {
		t.Fatalf("expected 4 headers, got %d", len(params.Headers))
	}
	if params.Headers[0] != "(request-target)" {
		t.Errorf("expected first header (request-target), got %s", params.Headers[0])
	}
}

func TestParseSignatureHeaderMissing(t *testing.T) {
	_, err := ParseSignatureHeader(`keyId="test",algorithm="rsa-sha256"`)
	if err == nil {
		t.Fatal("expected error for missing signature")
	}
}

func TestVerifyDigest(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	hash := sha256Sum(body)
	digest := "SHA-256=" + hash

	if err := verifyDigest(digest, body); err != nil {
		t.Errorf("verifyDigest failed: %v", err)
	}

	// Wrong body
	if err := verifyDigest(digest, []byte("wrong")); err == nil {
		t.Fatal("expected digest mismatch error")
	}
}

// sha256Sum returns the base64-encoded SHA-256 hash of the body.
func sha256Sum(body []byte) string {
	h := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(h[:])
}