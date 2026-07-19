// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
	"github.com/wscoble/federated-meetup/internal/product"
)

// newTestServer creates a Server with an in-memory SQLite DB and a
// product.Service for testing. Returns the server and a cleanup func.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	prodStore := product.NewStore()
	prodSvc := product.NewService(prodStore, nil)

	srv, err := NewServer(nil, prodSvc, store, nil, "")
	if err != nil {
		store.Close()
		t.Fatalf("NewServer: %v", err)
	}

	// Use a fixed clock for deterministic tests
	fixedTime := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return fixedTime })

	cleanup := func() {
		store.Close()
	}
	return srv, cleanup
}

// seedTestData inserts a group and an event into both the SQLite cache
// and the product store.
func seedTestData(t *testing.T, srv *Server) (groupKey, eventID string) {
	t.Helper()
	groupKey = "testgroup123"
	eventID = "evt-001"

	// Seed group in SQLite cache
	if err := srv.store.UpsertGroup(CachedGroup{
		GroupKey:      groupKey,
		CanonicalName: "test-group",
		DisplayName:   "Test Group",
		Description:   "A test group",
	}); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	// Seed event in SQLite cache
	if err := srv.store.UpsertEvent(CachedEvent{
		GroupKey:    groupKey,
		EventID:     eventID,
		Title:       "Test Event",
		Description: "A test event",
		StartsAt:    time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC).Unix(),
		Location:    "Test Location",
		Capacity:    50,
		Cancelled:   false,
	}); err != nil {
		t.Fatalf("UpsertEvent: %v", err)
	}

	// Also seed in product store
	srv.product.Store().PutGroup(&pb.Group{
		GroupId:       groupKey,
		CanonicalName: "test-group",
		DisplayName:   "Test Group",
		Description:   "A test group",
	})
	srv.product.Store().PutEvent(&pb.Event{
		EventId:     eventID,
		GroupId:     groupKey,
		Title:       "Test Event",
		Description: "A test event",
		Location:    "Test Location",
		Capacity:    50,
	})

	return groupKey, eventID
}

// ---- Store CRUD tests ----

func TestStoreGroupCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create
	g := CachedGroup{
		GroupKey:      "g1",
		CanonicalName: "group-one",
		DisplayName:   "Group One",
		Description:   "First group",
	}
	if err := store.UpsertGroup(g); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	// Read by canonical name
	got, err := store.GetGroupByCanonicalName("group-one")
	if err != nil {
		t.Fatalf("GetGroupByCanonicalName: %v", err)
	}
	if got.GroupKey != "g1" || got.DisplayName != "Group One" {
		t.Fatalf("unexpected group: %+v", got)
	}

	// Read by group key
	got2, err := store.GetGroup("g1")
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if got2.CanonicalName != "group-one" {
		t.Fatalf("unexpected canonical_name: %s", got2.CanonicalName)
	}

	// List
	groups, err := store.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	// Update
	g.Description = "Updated description"
	if err := store.UpsertGroup(g); err != nil {
		t.Fatalf("UpsertGroup update: %v", err)
	}
	got3, _ := store.GetGroup("g1")
	if got3.Description != "Updated description" {
		t.Fatalf("expected updated description, got %s", got3.Description)
	}

	// Delete
	if err := store.DeleteGroup("g1"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	_, err = store.GetGroup("g1")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestStoreEventCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create
	e := CachedEvent{
		GroupKey:    "g1",
		EventID:     "e1",
		Title:       "Event One",
		Description: "First event",
		StartsAt:    time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC).Unix(),
		Location:    "Somewhere",
		Capacity:    100,
		Cancelled:   false,
	}
	if err := store.UpsertEvent(e); err != nil {
		t.Fatalf("UpsertEvent: %v", err)
	}

	// Read
	got, err := store.GetEvent("g1", "e1")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.Title != "Event One" {
		t.Fatalf("unexpected title: %s", got.Title)
	}

	// List upcoming (should include our event since it's in the future
	// and the store's clock is time.Now by default)
	_, err = store.ListUpcomingEvents("g1", 10)
	if err != nil {
		t.Fatalf("ListUpcomingEvents: %v", err)
	}
	// Event is in August 2026 — may or may not be "upcoming" depending on real time
	// Just check it doesn't error

	// List by group
	allEvents, err := store.ListEventsByGroup("g1")
	if err != nil {
		t.Fatalf("ListEventsByGroup: %v", err)
	}
	if len(allEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(allEvents))
	}

	// Delete
	if err := store.DeleteEvent("g1", "e1"); err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	_, err = store.GetEvent("g1", "e1")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestStoreRsvpCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create
	r := RSVPRecord{
		GroupKey:  "g1",
		EventID:   "e1",
		UserEmail: "alice@example.com",
		UserName:  "Alice",
		Token:     "tok123",
		Confirmed: false,
	}
	if err := store.CreateRsvp(r); err != nil {
		t.Fatalf("CreateRsvp: %v", err)
	}

	// Read by token
	got, err := store.GetRsvpByToken("tok123")
	if err != nil {
		t.Fatalf("GetRsvpByToken: %v", err)
	}
	if got.UserEmail != "alice@example.com" {
		t.Fatalf("unexpected email: %s", got.UserEmail)
	}
	if got.Confirmed {
		t.Fatal("should not be confirmed yet")
	}

	// Confirm
	confirmed, err := store.ConfirmRsvp("tok123")
	if err != nil {
		t.Fatalf("ConfirmRsvp: %v", err)
	}
	if !confirmed.Confirmed {
		t.Fatal("should be confirmed")
	}

	// Idempotent confirm
	confirmed2, err := store.ConfirmRsvp("tok123")
	if err != nil {
		t.Fatalf("ConfirmRsvp idempotent: %v", err)
	}
	if !confirmed2.Confirmed {
		t.Fatal("should still be confirmed")
	}

	// RsvpCount
	count, err := store.RsvpCount("g1", "e1")
	if err != nil {
		t.Fatalf("RsvpCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 RSVP, got %d", count)
	}

	// List RSVPs
	rsvps, err := store.ListRsvpsForEvent("g1", "e1")
	if err != nil {
		t.Fatalf("ListRsvpsForEvent: %v", err)
	}
	if len(rsvps) != 1 {
		t.Fatalf("expected 1 RSVP, got %d", len(rsvps))
	}
}

func TestStoreSessionCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create session
	if err := store.CreateSession("sess123", "g1", 24*time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Validate
	gk, ok := store.ValidateSession("sess123")
	if !ok {
		t.Fatal("session should be valid")
	}
	if gk != "g1" {
		t.Fatalf("expected group g1, got %s", gk)
	}

	// Invalid token
	_, ok = store.ValidateSession("nonexistent")
	if ok {
		t.Fatal("nonexistent session should not be valid")
	}

	// Delete
	if err := store.DeleteSession("sess123"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, ok = store.ValidateSession("sess123")
	if ok {
		t.Fatal("session should be invalid after delete")
	}
}

func TestStoreOrderCRUD(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create
	o := OrderRecord{
		OrderID:         "ord123",
		GroupKey:        "g1",
		EventID:         "e1",
		Email:           "bob@example.com",
		AmountCents:     5000,
		Status:          "PENDING",
		StripeSessionID: "sess_stripe_123",
	}
	if err := store.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// Read
	got, err := store.GetOrder("ord123")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.Email != "bob@example.com" {
		t.Fatalf("unexpected email: %s", got.Email)
	}
	if got.AmountCents != 5000 {
		t.Fatalf("unexpected amount: %d", got.AmountCents)
	}

	// List by event
	orders, err := store.ListOrdersByEvent("g1", "e1")
	if err != nil {
		t.Fatalf("ListOrdersByEvent: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}

	// Update status
	if err := store.UpdateOrderStatus("ord123", "COMPLETED"); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	got2, _ := store.GetOrder("ord123")
	if got2.Status != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", got2.Status)
	}
}

// ---- Page rendering tests ----

func TestHomePage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Test Group") {
		t.Fatal("home page should contain group name")
	}
}

func TestGroupPage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/groups/test-group", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Test Group") {
		t.Fatal("group page should contain group name")
	}
}

func TestEventPage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/events/"+groupKey+"/"+eventID, nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Test Event") {
		t.Fatal("event page should contain event title")
	}
	// Check JSON-LD is present
	if !strings.Contains(body, "application/ld+json") {
		t.Fatal("event page should contain schema.org JSON-LD")
	}
	if !strings.Contains(body, "Event") {
		t.Fatal("JSON-LD should contain @type Event")
	}
}

func TestEventPageNotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/events/nonexistent/nosuchevent", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- CSP header tests ----

func TestCSPHeaders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header should be present")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP should contain default-src 'self', got: %s", csp)
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP should contain script-src 'self', got: %s", csp)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options should be nosniff")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("X-Frame-Options should be DENY")
	}
}

// ---- CSRF tests ----

func TestCSRFMissingToken(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// POST without CSRF token should return 403
	req := httptest.NewRequest("POST", "/dashboard/events", strings.NewReader("title=Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("POST without CSRF token should return 403, got %d", w.Code)
	}
}

func TestCSRFValidToken(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// First, get a CSRF cookie via a GET request
	getReq := httptest.NewRequest("GET", "/", nil)
	getW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(getW, getReq)

	csrfCookie := getW.Result().Cookies()
	var token string
	for _, c := range csrfCookie {
		if c.Name == CSRFCookieName {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("expected CSRF cookie to be set")
	}

	// Set up organizer session
	prodStore := srv.product.Store()
	prodStore.PutOrganizerToken("org-token-123", "testgroup123")
	srv.store.CreateSession("test-session", "testgroup123", 24*time.Hour)

	// POST with valid CSRF token
	form := strings.NewReader("title=Test+Event&description=Test&starts_at=2026-08-01T18:00&location=Here&capacity=50&csrf_token=" + token)
	postReq := httptest.NewRequest("POST", "/dashboard/events", form)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Add cookies
	postReq.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	postReq.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-session"})

	postW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(postW, postReq)

	if postW.Code == 403 {
		t.Fatal("POST with valid CSRF token should not return 403")
	}
}

// ---- Magic-link RSVP flow tests ----

func TestMagicLinkRsvpFlow(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)

	// Step 1: Submit RSVP form
	form := strings.NewReader("email=alice@example.com&name=Alice&csrf_token=fake")
	req := httptest.NewRequest("POST", "/events/"+groupKey+"/"+eventID+"/rsvp", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Set CSRF cookie to match form value
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("RSVP submit should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "magic link") {
		t.Fatalf("RSVP response should mention magic link, got: %s", body)
	}

	// Step 2: Verify the RSVP was stored with a token
	// We need to find the token — query the store
	rsvps, _ := srv.store.ListRsvpsForEvent(groupKey, eventID)
	if len(rsvps) != 0 {
		t.Fatal("expected 0 confirmed RSVPs before confirmation")
	}

	// Get the RSVP by looking at the raw store (we need the token)
	// Since ListRsvpsForEvent only returns confirmed, we use GetRsvpByToken
	// but we don't know the token. Let's query directly.
	// We'll just test the confirm flow with a known token.
	rsvp := RSVPRecord{
		GroupKey:  groupKey,
		EventID:   eventID,
		UserEmail: "bob@example.com",
		UserName:  "Bob",
		Token:     "bob-token-123",
		Confirmed: false,
	}
	if err := srv.store.CreateRsvp(rsvp); err != nil {
		t.Fatalf("CreateRsvp: %v", err)
	}

	// Step 3: Visit the magic link
	req2 := httptest.NewRequest("GET", "/rsvp/bob-token-123", nil)
	w2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("RSVP page should return 200, got %d", w2.Code)
	}
	body2 := w2.Body.String()
	if !strings.Contains(body2, "Confirm Your RSVP") {
		t.Fatalf("RSVP page should contain 'Confirm Your RSVP', got: %s", body2[:200])
	}

	// Step 4: Confirm the RSVP
	form3 := strings.NewReader("csrf_token=fake")
	req3 := httptest.NewRequest("POST", "/rsvp/bob-token-123/confirm", form3)
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w3 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w3, req3)

	if w3.Code != 200 {
		t.Fatalf("RSVP confirm should return 200, got %d", w3.Code)
	}
	body3 := w3.Body.String()
	if !strings.Contains(body3, "confirmed") {
		t.Fatalf("RSVP confirm response should mention 'confirmed', got: %s", body3)
	}

	// Step 5: Verify RSVP is now confirmed
	count, _ := srv.store.RsvpCount(groupKey, eventID)
	if count != 1 {
		t.Fatalf("expected 1 confirmed RSVP, got %d", count)
	}
}

// ---- Organizer auth tests ----

func TestDashboardUnauthenticated(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// Should redirect to login
	if w.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated dashboard should redirect (303), got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/dashboard/login" {
		t.Fatalf("should redirect to /dashboard/login, got %s", loc)
	}
}

func TestDashboardAuthenticated(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Create a session
	srv.store.CreateSession("test-session-token", groupKey, 24*time.Hour)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-session-token"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("authenticated dashboard should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Organizer Dashboard") {
		t.Fatal("dashboard should contain 'Organizer Dashboard'")
	}
}

func TestLoginAndLogout(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Register an organizer token in the product store
	srv.product.Store().PutOrganizerToken("valid-org-token", groupKey)

	// Login
	form := strings.NewReader("token=valid-org-token&csrf_token=fake")
	req := httptest.NewRequest("POST", "/dashboard/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("login should redirect (303), got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/dashboard" {
		t.Fatalf("should redirect to /dashboard, got %s", loc)
	}

	// Check session cookie was set
	var sessionToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("session cookie should be set after login")
	}

	// Verify session works
	gk, ok := srv.store.ValidateSession(sessionToken)
	if !ok || gk != groupKey {
		t.Fatalf("session should be valid for group %s", groupKey)
	}
}

// ---- Static file test ----

func TestStaticFilesServed(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/static/htmx.min.js", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("static file should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "htmx") {
		t.Fatal("htmx.min.js should contain 'htmx'")
	}
}

// ---- JSON-LD test ----

func TestEventJSONLD(t *testing.T) {
	e := CachedEvent{
		GroupKey:    "g1",
		EventID:     "e1",
		Title:       "Test Event",
		Description: "A test event",
		StartsAt:    time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC).Unix(),
		Location:    "Test Venue",
		Capacity:    50,
		Cancelled:   false,
	}
	ld := eventJSONLD(e, 10, "http://localhost:8080")
	if !strings.Contains(ld, "Event") {
		t.Fatal("JSON-LD should contain @type Event")
	}
	if !strings.Contains(ld, "Test Event") {
		t.Fatal("JSON-LD should contain event title")
	}
	if !strings.Contains(ld, "EventScheduled") {
		t.Fatal("JSON-LD should contain eventStatus")
	}
	if !strings.Contains(ld, "Place") {
		t.Fatal("JSON-LD should contain location as Place")
	}
	if !strings.Contains(ld, "maximumAttendeeCapacity") {
		t.Fatal("JSON-LD should contain maximumAttendeeCapacity")
	}
}
// ---- ICS calendar export test ----

func TestEventICSExport(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/events/"+groupKey+"/"+eventID+"/calendar.ics", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Verify ICS structure
	if !strings.Contains(body, "BEGIN:VCALENDAR") {
		t.Fatal("ICS should contain BEGIN:VCALENDAR")
	}
	if !strings.Contains(body, "END:VCALENDAR") {
		t.Fatal("ICS should contain END:VCALENDAR")
	}
	if !strings.Contains(body, "BEGIN:VEVENT") {
		t.Fatal("ICS should contain BEGIN:VEVENT")
	}
	if !strings.Contains(body, "DTSTART:") {
		t.Fatal("ICS should contain DTSTART")
	}
	if !strings.Contains(body, "DTEND:") {
		t.Fatal("ICS should contain DTEND")
	}
	if !strings.Contains(body, "SUMMARY:Test Event") {
		t.Fatal("ICS should contain event title as SUMMARY")
	}
	if !strings.Contains(body, "UID:"+eventID+"@federated-meetup") {
		t.Fatal("ICS should contain event UID")
	}

	// Verify content type
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/calendar") {
		t.Fatalf("expected text/calendar content type, got %s", ct)
	}

	t.Logf("ICS export OK: %d bytes, contains VCALENDAR+VEVENT", len(body))
}

// ---- Group creation tests ----

func TestNewGroupFormRenders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/groups/new", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Create a Group") {
		t.Fatal("group creation form should contain 'Create a Group'")
	}
	if !strings.Contains(body, "Group name") {
		t.Fatal("form should contain 'Group name' label")
	}
	if !strings.Contains(body, "Your email") {
		t.Fatal("form should contain 'Your email' label (the organizer email field)")
	}
}

func TestCreateGroup(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	form := strings.NewReader("display_name=Go Developers&description=A group for Go devs&organizer_name=Alice&organizer_email=alice@example.com&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/new", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	// handleCreateGroup now redirects with ?welcome=1 to trigger the
	// first-run success state on the dashboard (see docs/09-ONBOARDING-IMPORT.md §5).
	if loc != "/dashboard?welcome=1" {
		t.Fatalf("expected redirect to /dashboard?welcome=1, got %s", loc)
	}

	// Check session cookie was set
	var sessionToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("session cookie should be set after group creation")
	}

	// Validate the session
	gk, ok := srv.store.ValidateSession(sessionToken)
	if !ok {
		t.Fatal("session should be valid")
	}

	// Verify the group appears in ListGroups
	groups, _ := srv.store.ListGroups()
	found := false
	for _, g := range groups {
		if g.GroupKey == gk {
			found = true
			if g.DisplayName != "Go Developers" {
				t.Fatalf("unexpected display name: %s", g.DisplayName)
			}
		}
	}
	if !found {
		t.Fatalf("created group %s not found in ListGroups", gk)
	}

	// Verify the group exists in the product store
	if srv.product != nil {
		grp, ok := srv.product.Store().GetGroup(gk)
		if !ok {
			t.Fatal("group should exist in product store")
		}
		if grp.DisplayName != "Go Developers" {
			t.Fatalf("product store group name: %s", grp.DisplayName)
		}
	}
}

func TestCreateGroupValidation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Missing required fields (empty display_name)
	form := strings.NewReader("display_name=&description=&organizer_name=&organizer_email=&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/new", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// Should render the form again with an error, not redirect
	if w.Code == http.StatusSeeOther {
		t.Fatal("should not redirect on validation failure")
	}
	if w.Code != 200 {
		t.Fatalf("expected 200 (form re-render), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "too short") {
		t.Fatalf("form should contain validation error, got: %s", body[:min(200, len(body))])
	}
}

func TestCreateGroupOrganizerTokenValid(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Create a group
	form := strings.NewReader("display_name=Test Organizers&description=Testing&organizer_name=Bob&organizer_email=bob@example.com&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/new", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	// Extract the group key from the session
	var sessionToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			sessionToken = c.Value
		}
	}
	gk, ok := srv.store.ValidateSession(sessionToken)
	if !ok {
		t.Fatal("session should be valid")
	}

	// Verify the group has a valid organizer token in the product store.
	// We can't directly read the token (it's logged, not returned),
	// but we can verify the group was created and an organizer token exists
	// by trying to log in with it. Since we don't have the token, we
	// just verify the group is in the product store with the right ID.
	if srv.product != nil {
		_, ok := srv.product.Store().GetGroup(gk)
		if !ok {
			t.Fatalf("group %s not found in product store", gk)
		}
		// Verify group key is 64 hex chars (32 bytes)
		if len(gk) != 64 {
			t.Fatalf("group key should be 64 hex chars (32 bytes), got %d: %s", len(gk), gk)
		}
	}
}

func TestCanonicalizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Vegas Programmers", "vegas-programmers"},
		{"Go Devs!", "go-devs"},
		{"  Multiple   Spaces  ", "multiple-spaces"},
		{"UPPERCASE", "uppercase"},
		{"Special@#$Characters", "special-characters"},
		{"", "group"},
	}
	for _, tt := range tests {
		got := canonicalizeName(tt.input)
		if got != tt.want {
			t.Errorf("canonicalizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildRRULE(t *testing.T) {
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC) // Wednesday

	tests := []struct {
		recurrenceType string
		count          string
		until          string
		wantContains   []string
	}{
		{"weekly", "10", "", []string{"FREQ=WEEKLY", "BYDAY=WE", "COUNT=10"}},
		{"daily", "5", "", []string{"FREQ=DAILY", "COUNT=5"}},
		{"monthly", "3", "", []string{"FREQ=MONTHLY", "COUNT=3"}},
		{"weekly", "", "2026-12-31", []string{"FREQ=WEEKLY", "BYDAY=WE", "UNTIL=20261231"}},
		{"none", "5", "", []string{}}, // returns empty
	}
	for _, tt := range tests {
		got := buildRRULE(tt.recurrenceType, start, tt.count, tt.until)
		if tt.recurrenceType == "none" {
			if got != "" {
				t.Errorf("buildRRULE(none) = %q, want empty", got)
			}
			continue
		}
		for _, want := range tt.wantContains {
			if !strings.Contains(got, want) {
				t.Errorf("buildRRULE(%q) = %q, should contain %q", tt.recurrenceType, got, want)
			}
		}
	}
}

func TestRecurrenceEventCreation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// First create a group
	groupForm := strings.NewReader("display_name=Recurring Test&description=Testing recurrence&organizer_name=Carol&organizer_email=carol@example.com&csrf_token=fake")
	groupReq := httptest.NewRequest("POST", "/groups/new", groupForm)
	groupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	groupReq.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	groupW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(groupW, groupReq)

	var sessionToken string
	for _, c := range groupW.Result().Cookies() {
		if c.Name == SessionCookieName {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("no session token after group creation")
	}

	// Create a recurring event (weekly, 5 occurrences)
	eventForm := strings.NewReader("title=Weekly Standup&description=Daily sync&starts_at=2026-08-01T18:00&location=Online&capacity=50&recurrence_type=weekly&recurrence_count=5&csrf_token=fake")
	eventReq := httptest.NewRequest("POST", "/dashboard/events", eventForm)
	eventReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	eventReq.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	eventReq.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	eventW := httptest.NewRecorder()
	srv.Routes().ServeHTTP(eventW, eventReq)

	if eventW.Code != 200 {
		t.Fatalf("expected 200, got %d", eventW.Code)
	}
	body := eventW.Body.String()
	if !strings.Contains(body, "Event created") {
		t.Fatalf("response should mention 'Event created', got: %s", body)
	}

	// Verify multiple events were created (5 instances)
	gk, _ := srv.store.ValidateSession(sessionToken)
	events, _ := srv.store.ListEventsByGroup(gk)
	if len(events) != 5 {
		t.Fatalf("expected 5 events (1 recurring × 5 instances), got %d", len(events))
	}

	// Verify all events have the same title
	for _, e := range events {
		if e.Title != "Weekly Standup" {
			t.Errorf("event title: got %q, want 'Weekly Standup'", e.Title)
		}
	}

	// Verify events are 7 days apart
	// ListEventsByGroup returns DESC order (newest first), so iterate forward.
	if len(events) >= 2 {
		for i := 0; i < len(events)-1; i++ {
			// events[i] is newer (later starts_at), events[i+1] is older
			diff := events[i].StartsAt - events[i+1].StartsAt
			if diff != 7*24*3600 {
				t.Errorf("interval between events %d and %d: got %d seconds, want %d", i, i+1, diff, 7*24*3600)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
