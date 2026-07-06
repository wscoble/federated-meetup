// SPDX-License-Identifier: AGPL-3.0
//
// Security regression tests for the /my-rsvps anti-dox flow.
//
// These tests pin down the security model after the 2026-07-05
// IDOR-by-email fix. They construct the attacks that were possible
// BEFORE the fix and assert that they are now REJECTED. They also
// cover the legitimate happy path: a user who knows their email
// and clicks the magic link in their inbox can see their RSVPs.
//
// If any of these tests fail after a refactor, treat that as a
// security regression — the fix has been undone.

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureSender is a test-only email sender that records every email
// for later inspection. NOT used by every test in this file — most
// tests use the default noop sender via newTestServer and reach into
// the store directly for the magic-link session token. Kept here for
// future tests that want to assert on email body content (e.g. that
// the my-rsvps-link email is content-free of RSVP data).
type captureSender struct {
	mu   sync.Mutex
	Sent []capturedEmail
}

type capturedEmail struct {
	To      string
	Subject string
	Body    string
}

func (c *captureSender) Send(_ context.Context, to, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Sent = append(c.Sent, capturedEmail{To: to, Subject: subject, Body: body})
	return nil
}

// seedRsvp inserts a confirmed RSVP for the given email and returns
// the row's token (which the test will use to attempt cancellation).
func seedRsvp(t *testing.T, srv *Server, groupKey, eventID, email, token string) {
	t.Helper()
	if err := srv.store.CreateRsvp(RSVPRecord{
		GroupKey:  groupKey,
		EventID:   eventID,
		UserEmail: email,
		UserName:  "Test User",
		Token:     token,
		Confirmed: true,
	}); err != nil {
		t.Fatalf("seed CreateRsvp: %v", err)
	}
}

// ---- Test 1: IDOR by email on GET /my-rsvps ----
//
// The classic attack: an attacker visits /my-rsvps?email=victim@x.com
// and gets the victim's attendance list. Before the fix, this would
// render the full list. After the fix, an ?email=... query alone
// reveals nothing; only ?token=... works, and the token is sent to
// the inbox of the email it authorizes.
func TestMyRsvps_IDOR_EmailParamDoesNotAuthorize(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	seedRsvp(t, srv, groupKey, eventID, "victim@example.com", "victim-rsvp-token-abc")

	// Attacker visits the old IDOR URL.
	req := httptest.NewRequest("GET", "/my-rsvps?email=victim@example.com", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 (form view), got %d", w.Code)
	}
	body := w.Body.String()
	// The page should render the email-entry form, NOT the victim's
	// RSVP list. "Test Event" is the event title we seeded.
	if strings.Contains(body, "Test Event") {
		t.Fatalf("IDOR REGRESSION: /my-rsvps?email=... disclosed the victim's RSVP. Body excerpt: %s",
			body[:min(500, len(body))])
	}
	// The page should mention the email-entry affordance (we're
	// showing the login form, not someone's data).
	if !strings.Contains(body, "Email") {
		t.Fatalf("expected form view (email-entry), got body: %s", body[:min(500, len(body))])
	}
}

// ---- Test 2: IDOR on POST /my-rsvps/cancel ----
//
// The classic attack: an attacker who knows a victim's email and
// (group_key, event_id) can cancel their RSVP. Before the fix, this
// was a single POST with email+group_key+event_id. After the fix,
// the only accepted credential is the unguessable rsvp_token.
func TestCancelRsvp_IDOR_EmailPlusEventIDDoesNotAuthorize(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	const token = "victim-rsvp-token-xyz"
	seedRsvp(t, srv, groupKey, eventID, "victim@example.com", token)

	// Old IDOR payload — must now be rejected.
	form := strings.NewReader(
		"email=victim@example.com&" +
			"group_key=" + groupKey + "&" +
			"event_id=" + eventID + "&" +
			"csrf_token=fake",
	)
	req := httptest.NewRequest("POST", "/my-rsvps/cancel", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// Without the rsvp_token, the handler must reject (400).
	if w.Code != http.StatusBadRequest {
		t.Fatalf("IDOR REGRESSION: cancel with email+group_key+event_id should be 400, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Verify the RSVP is STILL confirmed (the attack didn't work).
	rsvp, err := srv.store.GetRsvpByToken(token)
	if err != nil {
		t.Fatalf("RSVP should still exist: %v", err)
	}
	if !rsvp.Confirmed {
		t.Fatal("IDOR REGRESSION: RSVP was canceled without the rsvp_token")
	}
}

// ---- Test 3: legitimate happy path via magic link ----
//
// A user with the email enters it, gets a magic link in the inbox,
// clicks it, and sees their RSVPs. This must keep working.
func TestMyRsvps_HappyPath_MagicLink(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	seedRsvp(t, srv, groupKey, eventID, "alice@example.com", "alice-rsvp-token")

	// Step 1: GET /my-rsvps shows the email-entry form.
	{
		req := httptest.NewRequest("GET", "/my-rsvps", nil)
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("step 1: expected 200, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "Email me a sign-in link") {
			t.Fatal("step 1: expected email-entry form")
		}
	}

	// Step 2: POST /my-rsvps/login {email} issues a session and
	// renders the "check your email" page.
	var sessionToken string
	{
		form := strings.NewReader("email=alice@example.com&csrf_token=fake")
		req := httptest.NewRequest("POST", "/my-rsvps/login", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("step 2: expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Check your email") {
			t.Fatal("step 2: expected 'check your email' confirmation")
		}

		// Pull the session token directly from the store. (We don't
		// intercept email in this test — the in-process sender is
		// a noop. The token is bound to the email and has 24h TTL.)
		// Use QueryRow so the underlying connection is released
		// before the next HTTP request; holding a *sql.Rows across
		// the request would deadlock under SetMaxOpenConns(1) (and
		// intermittently under SetMaxOpenConns(2)).
		row := srv.store.db.QueryRow("SELECT token FROM my_rsvps_sessions WHERE email = ?", "alice@example.com")
		if err := row.Scan(&sessionToken); err != nil {
			t.Fatalf("scan session token: %v", err)
		}
		if sessionToken == "" {
			t.Fatal("step 2: session token is empty")
		}
	}

	// Step 3: GET /my-rsvps?token=... returns the RSVP list.
	{
		req := httptest.NewRequest("GET", "/my-rsvps?token="+sessionToken, nil)
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("step 3: expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, "Test Event") {
			t.Fatal("step 3: expected the RSVP list to show the seeded event")
		}
		if !strings.Contains(body, "alice@example.com") {
			t.Fatal("step 3: expected the email to appear in the page header")
		}
	}

	// Step 4: the session is single-use; replaying the same token
	// shows the "link invalid" state, not the RSVP list.
	{
		req := httptest.NewRequest("GET", "/my-rsvps?token="+sessionToken, nil)
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)
		body := w.Body.String()
		if strings.Contains(body, "Showing RSVPs for") {
			t.Fatal("step 4: session should be single-use — replay disclosed RSVP list again")
		}
	}
}

// ---- Test 4: cancel via rsvp_token works (and burns the token) ----
func TestCancelRsvp_HappyPath_BurnsToken(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	const token = "burnable-rsvp-token-1234567890"
	seedRsvp(t, srv, groupKey, eventID, "carol@example.com", token)

	// First cancel: succeeds.
	form := strings.NewReader("rsvp_token=" + token + "&csrf_token=fake")
	req := httptest.NewRequest("POST", "/my-rsvps/cancel", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("first cancel: expected 303 redirect, got %d (body: %s)", w.Code, w.Body.String())
	}

	// RSVP is gone (we burn the token by deleting the row).
	if _, err := srv.store.GetRsvpByToken(token); err == nil {
		t.Fatal("RSVP should be deleted after successful cancel")
	}

	// Second cancel: 404 (token not found).
	form2 := strings.NewReader("rsvp_token=" + token + "&csrf_token=fake")
	req2 := httptest.NewRequest("POST", "/my-rsvps/cancel", form2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("replay: expected 404, got %d (body: %s)", w2.Code, w2.Body.String())
	}
}

// ---- Test 5: invalid token does not leak whether the email has RSVPs ----
func TestMyRsvps_InvalidToken_DoesNotEnumerate(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, eventID := seedTestData(t, srv)
	seedRsvp(t, srv, groupKey, eventID, "realuser@example.com", "realuser-rsvp-token")

	// Attacker probes a bogus token.
	req := httptest.NewRequest("GET", "/my-rsvps?token=not-a-real-token", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// The page should show the "link invalid" hint, NOT the real
	// user's data.
	if strings.Contains(body, "realuser@example.com") {
		t.Fatal("ENUMERATION REGRESSION: invalid-token page disclosed real user's email")
	}
	if strings.Contains(body, "Test Event") {
		t.Fatal("ENUMERATION REGRESSION: invalid-token page disclosed real user's RSVP")
	}
	if !strings.Contains(body, "invalid") && !strings.Contains(body, "expired") {
		t.Fatal("expected a soft 'invalid/expired' hint on the page")
	}
}

// ---- Test 6: rate limiting kicks in for repeated /my-rsvps/login ----
//
// An attacker cycling through email addresses from a single IP must
// be throttled. We test the underlying primitive directly because
// the handler's rate limiter keys on r.RemoteAddr, which is
// localhost in tests and not useful for asserting on bursts.
func TestIPRateLimit_ThrottlesAfterBurst(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// 5 calls within the burst succeed.
	for i := 0; i < 5; i++ {
		if !srv.ipAllow("203.0.113.7", 0.2, 5) {
			t.Fatalf("call %d within burst should succeed", i+1)
		}
	}
	// 6th call from the same IP is throttled.
	if srv.ipAllow("203.0.113.7", 0.2, 5) {
		t.Fatal("6th call from same IP should be throttled (burst exhausted)")
	}
	// A different IP gets its own bucket and is allowed.
	if !srv.ipAllow("198.51.100.42", 0.2, 5) {
		t.Fatal("different IP should have its own bucket and be allowed")
	}
}

// ---- Test 7: expired session token is rejected ----
func TestMyRsvps_ExpiredSessionToken(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Create a session that is already expired.
	expiredTime := srv.now().Add(-1 * time.Hour).Unix()
	if _, err := srv.store.db.Exec(
		`INSERT INTO my_rsvps_sessions (token, email, expires_at, created_at)
		 VALUES (?, ?, ?, ?)`,
		"expired-tok", "bob@example.com", expiredTime, srv.now().Unix(),
	); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}

	req := httptest.NewRequest("GET", "/my-rsvps?token=expired-tok", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 (form view), got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "Showing RSVPs for") {
		t.Fatal("REGRESSION: expired token disclosed RSVP list")
	}
	// And the row should have been cleaned up.
	if _, err := srv.store.ConsumeMyRsvpsSession("expired-tok"); err == nil {
		t.Fatal("expired session row should be deleted after use attempt")
	}
}

// ---- Test 8: re-issuing for the same email overwrites the prior session ----
//
// This is a defense against a different attack: an attacker who
// issues a session for victim@example.com should not be able to
// use the OLD session after the victim re-issues. The
// email-UNIQUE constraint enforces this at the storage layer.
func TestMyRsvps_ReissueOverwritesPriorSession(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	if err := srv.store.CreateMyRsvpsSession("old-tok", "x@example.com", time.Hour); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := srv.store.CreateMyRsvpsSession("new-tok", "x@example.com", time.Hour); err != nil {
		t.Fatalf("re-issue: %v", err)
	}
	// Old token should no longer be findable.
	if _, err := srv.store.ConsumeMyRsvpsSession("old-tok"); err == nil {
		t.Fatal("old session should be overwritten on re-issue")
	}
	// New token should be valid.
	sess, err := srv.store.ConsumeMyRsvpsSession("new-tok")
	if err != nil {
		t.Fatalf("new session should be valid: %v", err)
	}
	if sess.Email != "x@example.com" {
		t.Fatalf("session email: got %q, want x@example.com", sess.Email)
	}
}

// ---- Test 9: login rate limit handler returns 429 under burst ----
//
// This is the end-to-end version of the primitive test. We use
// httptest's RemoteAddr (which httptest sets to "192.0.2.1:1234")
// and exhaust the burst. (Note: we exhaust the per-IP bucket from
// the test runner's "192.0.2.1" IP — the burst is 5, so 6 calls
// should hit 429.)
func TestMyRsvpsLogin_RateLimitReturns429(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// 5 calls within the burst (the bucket is 5). All return 200.
	for i := 0; i < 5; i++ {
		form := strings.NewReader("email=enumtarget@example.com&csrf_token=fake")
		req := httptest.NewRequest("POST", "/my-rsvps/login", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
		req.RemoteAddr = "192.0.2.1:5555"
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("burst call %d: expected 200, got %d", i+1, w.Code)
		}
	}
	// 6th call: throttled.
	form := strings.NewReader("email=enumtarget@example.com&csrf_token=fake")
	req := httptest.NewRequest("POST", "/my-rsvps/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	req.RemoteAddr = "192.0.2.1:5555"
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst, got %d (body: %s)", w.Code, w.Body.String())
	}
}
