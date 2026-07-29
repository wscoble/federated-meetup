// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ---- Attendance check-in UI tests ----

func TestAttendanceHistoryPageAuth(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Unauthenticated → redirect to login
	req := httptest.NewRequest("GET", "/dashboard/attendance", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated attendance history should redirect (303), got %d", w.Code)
	}
	if w.Header().Get("Location") != "/dashboard/login" {
		t.Fatalf("should redirect to /dashboard/login, got %s", w.Header().Get("Location"))
	}
}

func TestAttendanceHistoryPageRenders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)
	srv.store.CreateSession("test-sess-ah", groupKey, 24*time.Hour)

	req := httptest.NewRequest("GET", "/dashboard/attendance", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-sess-ah"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("authenticated attendance history should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Attendance History") {
		t.Fatal("attendance history page should contain 'Attendance History'")
	}
}

func TestCheckInRendersFragment(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	srv.store.CreateSession("test-sess-ci", groupKey, 24*time.Hour)

	// Create a confirmed RSVP
	rsvp := RSVPRecord{
		GroupKey:  groupKey,
		EventID:   eventID,
		UserEmail: "alice@example.com",
		UserName:  "Alice",
		Token:     "alice-checkin-token",
		Confirmed: true,
	}
	if err := srv.store.CreateRsvp(rsvp); err != nil {
		t.Fatalf("CreateRsvp: %v", err)
	}

	// Perform check-in via HTMX
	form := strings.NewReader("event_id=" + eventID + "&email=alice@example.com&csrf_token=fake")
	req := httptest.NewRequest("POST", "/dashboard/events/"+groupKey+"/checkin", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-sess-ci"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("check-in should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "attendee-table") {
		t.Fatalf("check-in response should contain attendee-table fragment, got: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Checked In") {
		t.Fatalf("check-in response should show 'Checked In' badge, got: %s", body[:min(200, len(body))])
	}
}

func TestBulkCheckInRendersFragment(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	srv.store.CreateSession("test-sess-bci", groupKey, 24*time.Hour)

	// Create confirmed RSVPs
	rsvp1 := RSVPRecord{
		GroupKey: groupKey, EventID: eventID,
		UserEmail: "alice@example.com", UserName: "Alice",
		Token: "bulk-token-1", Confirmed: true,
	}
	rsvp2 := RSVPRecord{
		GroupKey: groupKey, EventID: eventID,
		UserEmail: "bob@example.com", UserName: "Bob",
		Token: "bulk-token-2", Confirmed: true,
	}
	if err := srv.store.CreateRsvp(rsvp1); err != nil {
		t.Fatalf("CreateRsvp 1: %v", err)
	}
	if err := srv.store.CreateRsvp(rsvp2); err != nil {
		t.Fatalf("CreateRsvp 2: %v", err)
	}

	form := strings.NewReader("event_id=" + eventID + "&csrf_token=fake")
	req := httptest.NewRequest("POST", "/dashboard/events/"+groupKey+"/bulk-checkin", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-sess-bci"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("bulk check-in should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "attendee-table") {
		t.Fatalf("bulk check-in response should contain attendee-table fragment")
	}
	// Both Alice and Bob should be checked in
	if !strings.Contains(body, "Checked In") {
		t.Fatal("bulk check-in response should show 'Checked In' badges")
	}
}

// ---- Video call button tests ----

func TestEventPageHasVideoCallButton(t *testing.T) {
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
	if !strings.Contains(body, "Join Video Call") {
		t.Fatal("event page should contain 'Join Video Call' button")
	}
	if !strings.Contains(body, "/video") {
		t.Fatal("event page should contain link to video room")
	}
	// Verify the link format
	expectedLink := "/events/" + groupKey + "/" + eventID + "/video"
	if !strings.Contains(body, expectedLink) {
		t.Fatalf("event page should contain link %s", expectedLink)
	}
}

// ---- CSP header with wss test ----

func TestCSPHeaderAllowsWebSocket(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "wss:") {
		t.Fatalf("CSP should allow wss: for WebSocket video signaling, got: %s", csp)
	}
	if !strings.Contains(csp, "ws:") {
		t.Fatalf("CSP should allow ws: for WebSocket video signaling, got: %s", csp)
	}
}

// ---- Assets page tests ----

func TestAssetsPageRenders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/groups/test-group/assets", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("assets page should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Shared Files") {
		t.Fatal("assets page should contain 'Shared Files'")
	}
	if !strings.Contains(body, "Upload a File") {
		t.Fatal("assets page should contain upload form")
	}
}

func TestAssetsPageEmptyState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/groups/test-group/assets", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No files yet") {
		t.Fatal("assets page with no assets should show empty state")
	}
}

func TestAssetsPageWithFile(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Create a test asset directly in the store
	_, err := srv.store.CreateAsset(GroupAsset{
		GroupKey:         groupKey,
		UploadedByEmail: "alice@example.com",
		Filename:         "test-file.pdf",
		OriginalFilename: "test-file.pdf",
		MimeType:         "application/pdf",
		SizeBytes:        1024,
		Category:         "study-notes",
		Description:      "Test study notes",
		StoredPath:       groupKey + "/test-file.pdf",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	req := httptest.NewRequest("GET", "/groups/test-group/assets", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test-file.pdf") {
		t.Fatal("assets page should list the uploaded file")
	}
	if !strings.Contains(body, "Download") {
		t.Fatal("assets page should have download link")
	}
}

// ---- Dashboard check-in UI tests ----

func TestDashboardHasCheckInButtons(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	srv.store.CreateSession("test-sess-dci", groupKey, 24*time.Hour)

	// Create a confirmed RSVP
	rsvp := RSVPRecord{
		GroupKey:  groupKey,
		EventID:   eventID,
		UserEmail: "alice@example.com",
		UserName:  "Alice",
		Token:     "dash-checkin-token",
		Confirmed: true,
	}
	if err := srv.store.CreateRsvp(rsvp); err != nil {
		t.Fatalf("CreateRsvp: %v", err)
	}

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-sess-dci"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("dashboard should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Check In") {
		t.Fatal("dashboard should have Check In button for attendee")
	}
	if !strings.Contains(body, "Bulk Check-In All") {
		t.Fatal("dashboard should have Bulk Check-In All button")
	}
	if !strings.Contains(body, "View Attendance History") {
		t.Fatal("dashboard should have attendance history link")
	}
}

// ---- Video room template test ----

func TestVideoRoomTemplateRenders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)

	// Set event start time to now so room is open — update both stores
	now := srv.now().Unix()
	srv.store.UpsertEvent(CachedEvent{
		GroupKey:    groupKey,
		EventID:     eventID,
		Title:       "Test Event",
		Description: "A test event",
		StartsAt:    now,
		Location:    "Test Location",
		Capacity:    50,
	})
	// Update the product store event too (handleVideoRoom reads from product store)
	srv.product.Store().PutEvent(&pb.Event{
		EventId:     eventID,
		GroupId:     groupKey,
		Title:       "Test Event",
		Description: "A test event",
		StartsAt:    timestamppb.New(srv.now()),
		Location:    "Test Location",
		Capacity:    50,
	})

	req := httptest.NewRequest("GET", "/events/"+groupKey+"/"+eventID+"/video?name=TestUser", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("video room should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Video Call") {
		t.Fatal("video room page should contain 'Video Call'")
	}
	if !strings.Contains(body, "TestUser") {
		t.Fatal("video room page should contain the peer name")
	}
}