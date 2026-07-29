// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- Store unit tests for prayer requests ----

func TestStorePrayerRequestCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Use a fixed clock for deterministic testing.
	fixedTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	// Create
	pr := PrayerRequest{
		GroupKey:         "g1",
		SubmittedByEmail: "alice@example.com",
		SubmittedByName:  "Alice",
		Title:            "Pray for healing",
		Body:              "Please pray for John's recovery from surgery.",
		Category:         PrayerCategoryHealing,
		Status:           PrayerStatusActive,
		IsAnonymous:      false,
	}
	id, err := store.CreatePrayerRequest(pr)
	if err != nil {
		t.Fatalf("CreatePrayerRequest: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Read
	got, err := store.GetPrayerRequest(id)
	if err != nil {
		t.Fatalf("GetPrayerRequest: %v", err)
	}
	if got.Title != "Pray for healing" {
		t.Fatalf("unexpected title: %s", got.Title)
	}
	if got.Category != PrayerCategoryHealing {
		t.Fatalf("unexpected category: %s", got.Category)
	}
	if got.Status != PrayerStatusActive {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if got.IsAnonymous {
		t.Fatal("should not be anonymous")
	}
	if got.GroupKey != "g1" {
		t.Fatalf("unexpected group_key: %s", got.GroupKey)
	}
	if got.CreatedAt != fixedTime.Unix() {
		t.Fatalf("unexpected created_at: %d", got.CreatedAt)
	}

	// List by group
	requests, err := store.ListPrayerRequests("g1")
	if err != nil {
		t.Fatalf("ListPrayerRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 prayer request, got %d", len(requests))
	}

	// List by status
	activeReqs, err := store.ListPrayerRequestsByStatus("g1", PrayerStatusActive)
	if err != nil {
		t.Fatalf("ListPrayerRequestsByStatus: %v", err)
	}
	if len(activeReqs) != 1 {
		t.Fatalf("expected 1 active prayer request, got %d", len(activeReqs))
	}

	// Update status
	if err := store.UpdatePrayerRequestStatus(id, PrayerStatusAnswered); err != nil {
		t.Fatalf("UpdatePrayerRequestStatus: %v", err)
	}
	got2, _ := store.GetPrayerRequest(id)
	if got2.Status != PrayerStatusAnswered {
		t.Fatalf("expected answered, got %s", got2.Status)
	}
	// updated_at should be >= created_at
	if got2.UpdatedAt < got2.CreatedAt {
		t.Fatal("updated_at should be >= created_at")
	}

	// Count
	count, err := store.CountPrayerRequests("g1")
	if err != nil {
		t.Fatalf("CountPrayerRequests: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}

func TestStorePrayerRequestDefaults(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create with empty category and status — should get defaults.
	pr := PrayerRequest{
		GroupKey: "g2",
		Title:    "General prayer",
		Body:     "A general prayer request.",
	}
	id, err := store.CreatePrayerRequest(pr)
	if err != nil {
		t.Fatalf("CreatePrayerRequest: %v", err)
	}
	got, _ := store.GetPrayerRequest(id)
	if got.Category != PrayerCategoryGeneral {
		t.Fatalf("expected default category 'general', got %s", got.Category)
	}
	if got.Status != PrayerStatusActive {
		t.Fatalf("expected default status 'active', got %s", got.Status)
	}
}

func TestStorePrayerRequestAnonymous(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create anonymous request
	pr := PrayerRequest{
		GroupKey:         "g3",
		SubmittedByEmail: "secret@example.com",
		SubmittedByName:  "Secret Person",
		Title:            "Anonymous prayer",
		Body:             "This is submitted anonymously.",
		IsAnonymous:      true,
	}
	id, err := store.CreatePrayerRequest(pr)
	if err != nil {
		t.Fatalf("CreatePrayerRequest: %v", err)
	}
	got, _ := store.GetPrayerRequest(id)
	if !got.IsAnonymous {
		t.Fatal("should be anonymous")
	}
	// The submitter info is stored in the DB (leaders can see it).
	if got.SubmittedByEmail != "secret@example.com" {
		t.Fatalf("email should be stored: got %s", got.SubmittedByEmail)
	}
	if got.SubmittedByName != "Secret Person" {
		t.Fatalf("name should be stored: got %s", got.SubmittedByName)
	}
}

func TestStorePrayerUpdateCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	fixedTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	// Create a prayer request first
	pr := PrayerRequest{
		GroupKey: "g4",
		Title:    "Pray for guidance",
		Body:     "Seeking direction for a career change.",
		Category: PrayerCategoryGuidance,
	}
	prID, err := store.CreatePrayerRequest(pr)
	if err != nil {
		t.Fatalf("CreatePrayerRequest: %v", err)
	}

	// Add an update
	pu := PrayerUpdate{
		PrayerRequestID: prID,
		UpdateText:      "Found a new opportunity — praying for wisdom to decide.",
		StatusChange:    PrayerStatusOngoing,
		CreatedBy:       "Bob",
	}
	updateID, err := store.CreatePrayerUpdate(pu)
	if err != nil {
		t.Fatalf("CreatePrayerUpdate: %v", err)
	}
	if updateID == 0 {
		t.Fatal("expected non-zero update ID")
	}

	// The parent request status should have changed.
	got, _ := store.GetPrayerRequest(prID)
	if got.Status != PrayerStatusOngoing {
		t.Fatalf("expected status 'ongoing' after update, got %s", got.Status)
	}

	// List updates
	updates, err := store.ListPrayerUpdates(prID)
	if err != nil {
		t.Fatalf("ListPrayerUpdates: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].UpdateText != "Found a new opportunity — praying for wisdom to decide." {
		t.Fatalf("unexpected update text: %s", updates[0].UpdateText)
	}
	if updates[0].StatusChange != PrayerStatusOngoing {
		t.Fatalf("unexpected status change: %s", updates[0].StatusChange)
	}

	// GetPrayerRequestsWithUpdates
	withUpdates, err := store.GetPrayerRequestsWithUpdates("g4")
	if err != nil {
		t.Fatalf("GetPrayerRequestsWithUpdates: %v", err)
	}
	if len(withUpdates) != 1 {
		t.Fatalf("expected 1 request with updates, got %d", len(withUpdates))
	}
	if len(withUpdates[0].Updates) != 1 {
		t.Fatalf("expected 1 update in nested struct, got %d", len(withUpdates[0].Updates))
	}
}

func TestStorePrayerUpdateWithoutStatusChange(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create a prayer request
	pr := PrayerRequest{
		GroupKey: "g5",
		Title:    "Pray for family",
		Category: PrayerCategoryFamily,
	}
	prID, _ := store.CreatePrayerRequest(pr)

	// Add update without status change (empty string)
	pu := PrayerUpdate{
		PrayerRequestID: prID,
		UpdateText:      "Just an update, no status change.",
		StatusChange:    "",
	}
	_, err = store.CreatePrayerUpdate(pu)
	if err != nil {
		t.Fatalf("CreatePrayerUpdate: %v", err)
	}

	// Status should remain "active" (the default).
	got, _ := store.GetPrayerRequest(prID)
	if got.Status != PrayerStatusActive {
		t.Fatalf("expected status to remain 'active', got %s", got.Status)
	}
}

func TestStoreStalePrayerRequests(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Set clock to a specific time and create a request.
	baseTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return baseTime })

	pr := PrayerRequest{
		GroupKey: "g6",
		Title:    "Old prayer request",
		Category: PrayerCategoryGeneral,
	}
	id1, _ := store.CreatePrayerRequest(pr)

	// Advance clock by 40 days and create another request.
	store.SetClock(func() time.Time { return baseTime.Add(40 * 24 * time.Hour) })
	pr2 := PrayerRequest{
		GroupKey: "g6",
		Title:    "Recent prayer request",
		Category: PrayerCategoryGeneral,
	}
	id2, _ := store.CreatePrayerRequest(pr2)

	// The first request should be stale (40 days old, not updated).
	// The second request is fresh (just created).
	stale, err := store.StalePrayerRequests("g6", 30)
	if err != nil {
		t.Fatalf("StalePrayerRequests: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale request, got %d", len(stale))
	}
	if stale[0].ID != id1 {
		t.Fatalf("expected stale request ID %d, got %d", id1, stale[0].ID)
	}

	// id2 should not be stale.
	for _, s := range stale {
		if s.ID == id2 {
			t.Fatal("recent request should not be in stale list")
		}
	}

	// Now mark id1 as answered — it should no longer be stale.
	store.UpdatePrayerRequestStatus(id1, PrayerStatusAnswered)
	stale2, _ := store.StalePrayerRequests("g6", 30)
	if len(stale2) != 0 {
		t.Fatalf("expected 0 stale after marking answered, got %d", len(stale2))
	}
}

func TestStorePrayerRequestGroupScoped(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create requests in two different groups.
	store.CreatePrayerRequest(PrayerRequest{GroupKey: "groupA", Title: "Prayer A"})
	store.CreatePrayerRequest(PrayerRequest{GroupKey: "groupB", Title: "Prayer B"})

	// List for groupA should only return groupA's requests.
	requests, _ := store.ListPrayerRequests("groupA")
	if len(requests) != 1 {
		t.Fatalf("expected 1 request for groupA, got %d", len(requests))
	}
	if requests[0].GroupKey != "groupA" {
		t.Fatalf("expected group_key groupA, got %s", requests[0].GroupKey)
	}

	// List for groupB should only return groupB's requests.
	requestsB, _ := store.ListPrayerRequests("groupB")
	if len(requestsB) != 1 {
		t.Fatalf("expected 1 request for groupB, got %d", len(requestsB))
	}
}

func TestValidatePrayerCategory(t *testing.T) {
	valid := []string{"general", "healing", "family", "work", "guidance", "thanksgiving"}
	for _, c := range valid {
		if !validatePrayerCategory(c) {
			t.Errorf("category %s should be valid", c)
		}
	}
	if validatePrayerCategory("invalid") {
		t.Error("invalid category should not be valid")
	}
}

func TestValidatePrayerStatus(t *testing.T) {
	valid := []string{"active", "answered", "ongoing", "archived"}
	for _, s := range valid {
		if !validatePrayerStatus(s) {
			t.Errorf("status %s should be valid", s)
		}
	}
	if validatePrayerStatus("invalid") {
		t.Error("invalid status should not be valid")
	}
}

// ---- Handler tests ----

func TestPrayerListPageRenders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Add a prayer request so the page has content.
	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:         "testgroup123",
		SubmittedByEmail: "alice@example.com",
		SubmittedByName:  "Alice",
		Title:            "Pray for healing",
		Body:             "John needs surgery.",
		Category:         PrayerCategoryHealing,
	})

	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Prayer Requests") {
		t.Fatal("prayer list page should contain 'Prayer Requests'")
	}
	if !strings.Contains(body, "Pray for healing") {
		t.Fatal("prayer list page should contain the prayer request title")
	}
	if !strings.Contains(body, "Healing") {
		t.Fatal("prayer list page should show the category badge")
	}
}

func TestPrayerListEmptyState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No prayer requests yet") {
		t.Fatal("empty prayer list should show empty state")
	}
}

func TestPrayerSubmitHandler(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	form := strings.NewReader("title=Pray for the community&body=Please pray for our neighborhood.&category=general&email=alice@example.com&submitter_name=Alice&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/test-group/prayer-list/submit", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Pray for the community") {
		t.Fatalf("response should contain the prayer request title, got: %s", body[:min(200, len(body))])
	}

	// Verify it was stored.
	requests, _ := srv.store.ListPrayerRequests("testgroup123")
	if len(requests) != 1 {
		t.Fatalf("expected 1 prayer request in store, got %d", len(requests))
	}
	if requests[0].Title != "Pray for the community" {
		t.Fatalf("unexpected title: %s", requests[0].Title)
	}
}

func TestPrayerSubmitValidation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Empty title should return an error.
	form := strings.NewReader("title=&body=test&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/test-group/prayer-list/submit", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "too short") && !strings.Contains(body, "error") {
		t.Fatalf("expected validation error, got: %s", body[:min(200, len(body))])
	}

	// No request should have been stored.
	requests, _ := srv.store.ListPrayerRequests("testgroup123")
	if len(requests) != 0 {
		t.Fatalf("expected 0 prayer requests, got %d", len(requests))
	}
}

func TestPrayerUpdateHandler(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create a prayer request directly in the store.
	prID, _ := srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:        "testgroup123",
		Title:           "Pray for John's surgery",
		Body:            "John is having surgery next week.",
		Category:        PrayerCategoryHealing,
		IsAnonymous:     false,
	})

	// Submit an update via HTMX.
	form := strings.NewReader("update_text=Surgery went well — John is recovering.&status_change=answered&created_by=Bob&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/test-group/prayer-list/"+strconv.FormatInt(prID, 10)+"/update", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Surgery went well") {
		t.Fatalf("response should contain the update text, got: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Answered") {
		t.Fatal("response should show the updated status badge")
	}

	// Verify the update was stored.
	updates, _ := srv.store.ListPrayerUpdates(prID)
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].UpdateText != "Surgery went well — John is recovering." {
		t.Fatalf("unexpected update text: %s", updates[0].UpdateText)
	}

	// Verify the parent request's status was updated.
	pr, _ := srv.store.GetPrayerRequest(prID)
	if pr.Status != PrayerStatusAnswered {
		t.Fatalf("expected status 'answered', got %s", pr.Status)
	}
}

func TestPrayerUpdateGroupScoped(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create a prayer request in group "testgroup123".
	prID, _ := srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey: "testgroup123",
		Title:    "Test prayer",
	})

	// Create a second group and try to access the prayer request via the wrong group.
	srv.store.UpsertGroup(CachedGroup{
		GroupKey:      "othergroup",
		CanonicalName: "other-group",
		DisplayName:   "Other Group",
	})

	// Attempt update from the wrong group — should return "not found" error.
	form := strings.NewReader("update_text=Hacked&status_change=archived&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/other-group/prayer-list/"+strconv.FormatInt(prID, 10)+"/update", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "not found") {
		t.Fatalf("cross-group access should return 'not found', got: %s", body[:min(200, len(body))])
	}

	// Verify the status was NOT changed.
	pr, _ := srv.store.GetPrayerRequest(prID)
	if pr.Status != PrayerStatusActive {
		t.Fatalf("status should not have changed, got %s", pr.Status)
	}
}

func TestPrayerPrintPage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Add a prayer request.
	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:        "testgroup123",
		Title:           "Pray for the community",
		Body:            "Please pray for our neighborhood.",
		Category:        PrayerCategoryGeneral,
		SubmittedByName: "Alice",
	})

	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list/print", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Prayer List") {
		t.Fatal("print page should contain 'Prayer List' heading")
	}
	if !strings.Contains(body, "Pray for the community") {
		t.Fatal("print page should contain the prayer request title")
	}
	// Should NOT contain the base template chrome (no nav, no footer).
	if strings.Contains(body, "Federated Meetup</a>") {
		t.Fatal("print page should not contain site navigation chrome")
	}
}

// ---- Anonymity tests ----

func TestPrayerAnonymousNonLeader(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create an anonymous prayer request.
	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:         "testgroup123",
		SubmittedByEmail: "secret@example.com",
		SubmittedByName:  "Secret Person",
		Title:            "Anonymous prayer request",
		Body:             "This is anonymous.",
		IsAnonymous:      true,
	})

	// View as a non-leader (no session cookie).
	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Anonymous") {
		t.Fatal("non-leader should see 'Anonymous' for anonymous requests")
	}
	if strings.Contains(body, "Secret Person") {
		t.Fatal("non-leader should NOT see the actual submitter name")
	}
	if strings.Contains(body, "secret@example.com") {
		t.Fatal("non-leader should NOT see the submitter email")
	}
}

func TestPrayerAnonymousLeader(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create an anonymous prayer request.
	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:         "testgroup123",
		SubmittedByEmail: "secret@example.com",
		SubmittedByName:  "Secret Person",
		Title:            "Anonymous prayer request",
		Body:             "This is anonymous.",
		IsAnonymous:      true,
	})

	// Set up a leader session.
	srv.store.CreateSession("leader-session", "testgroup123", 24*time.Hour)

	// View as a leader.
	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "leader-session"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	// Leader should see the actual name with the anonymous note.
	if !strings.Contains(body, "Secret Person") {
		t.Fatal("leader should see the actual submitter name for anonymous requests")
	}
	if !strings.Contains(body, "anonymous to group") {
		t.Fatal("leader should see the '(anonymous to group)' annotation")
	}
}

func TestPrayerNonAnonymousVisible(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create a non-anonymous prayer request.
	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey:         "testgroup123",
		SubmittedByEmail: "alice@example.com",
		SubmittedByName:  "Alice",
		Title:            "Public prayer",
		Body:             "This is not anonymous.",
		IsAnonymous:      false,
	})

	// View as non-leader.
	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Alice") {
		t.Fatal("non-leader should see the submitter name for non-anonymous requests")
	}
}

// ---- Prayer follow-up (stale) tests ----

func TestPrayerFollowUpLeaderDashboard(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create a stale prayer request (40 days ago).
	baseTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return baseTime })

	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey: "testgroup123",
		Title:    "Old prayer needing follow-up",
		Category: PrayerCategoryHealing,
	})

	// Advance clock 40 days.
	srv.SetClock(func() time.Time { return baseTime.Add(40 * 24 * time.Hour) })

	// Set up leader session.
	srv.store.CreateSession("leader-session", "testgroup123", 24*time.Hour)

	// View as leader — should see the follow-up prompt.
	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "leader-session"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Prayer Follow-Up Needed") {
		t.Fatal("leader should see 'Prayer Follow-Up Needed' section")
	}
	if !strings.Contains(body, "Old prayer needing follow-up") {
		t.Fatal("leader should see the stale prayer request title")
	}
	if !strings.Contains(body, "Follow up?") {
		t.Fatal("leader should see the 'Follow up?' prompt")
	}
}

func TestPrayerFollowUpNonLeaderHidden(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Create a stale prayer request.
	baseTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return baseTime })

	srv.store.CreatePrayerRequest(PrayerRequest{
		GroupKey: "testgroup123",
		Title:    "Old prayer needing follow-up",
	})

	// Advance clock 40 days.
	srv.SetClock(func() time.Time { return baseTime.Add(40 * 24 * time.Hour) })

	// View as non-leader (no session cookie).
	req := httptest.NewRequest("GET", "/groups/test-group/prayer-list", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "Prayer Follow-Up Needed") {
		t.Fatal("non-leader should NOT see the 'Prayer Follow-Up Needed' section")
	}
	if strings.Contains(body, "Follow up?") {
		t.Fatal("non-leader should NOT see the 'Follow up?' prompt")
	}
}

// ---- Category validation in submit handler ----

func TestPrayerSubmitInvalidCategory(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)

	// Submit with an invalid category — should default to "general".
	form := strings.NewReader("title=Test&body=Test&category=invalid_cat&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/test-group/prayer-list/submit", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	requests, _ := srv.store.ListPrayerRequests("testgroup123")
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Category != PrayerCategoryGeneral {
		t.Fatalf("invalid category should default to 'general', got %s", requests[0].Category)
	}
}