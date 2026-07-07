// SPDX-License-Identifier: AGPL-3.0
//
// Regression tests for the 2026-07-06 security-audit hardening pass
// (PR-B). Pinned audit findings:
//
//   - C-3: dashboard endpoints do not verify event_id belongs to group_key
//   - H-4: To: header CRLF injection in SMTPSender
//   - M-5: no body size cap on public POST endpoints
//
// Each test below is a fail-before / pass-after regression. If any
// of these fail after a refactor, treat that as a security regression.

package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"

	emailpkg "github.com/wscoble/federated-meetup/internal/email"
)

// authedRequest is a helper that creates an httptest request with
// the session cookie set to a real session for groupKey. It also
// sets the CSRF cookie to "fake" so the csrfMiddleware accepts the
// form value "csrf_token=fake".
func authedRequest(t *testing.T, srv *Server, method, path, body, groupKey string) *http.Request {
	t.Helper()
	if err := srv.store.CreateSession("sess-"+groupKey, groupKey, 24*60*60*1000*1000*1000); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "sess-" + groupKey})
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	return req
}

// seedTwoGroupsWithOneEvent seeds two distinct groups each with one
// event. Returns the keys/event-ids so the test can name the
// "attacker's" group and the "victim's" group unambiguously.
func seedTwoGroupsWithOneEvent(t *testing.T, srv *Server) (attackerKey, victimKey, victimEventID string) {
	t.Helper()

	attackerKey = "attacker-group-key"
	victimKey = "victim-group-key"
	victimEventID = "victim-event-id-1"

	// Seed both groups in BOTH product store and SQLite cache so
	// the C-3 ownership check has data in both.
	srv.product.Store().PutGroup(&pb.Group{GroupId: attackerKey, DisplayName: "Attacker"})
	srv.product.Store().PutGroup(&pb.Group{GroupId: victimKey, DisplayName: "Victim"})
	srv.product.Store().PutEvent(&pb.Event{
		EventId: victimEventID,
		GroupId: victimKey,
		Title:   "Victim Event",
	})
	if err := srv.store.UpsertGroup(CachedGroup{GroupKey: attackerKey, DisplayName: "Attacker", CanonicalName: "attacker"}); err != nil {
		t.Fatalf("seed attacker group: %v", err)
	}
	if err := srv.store.UpsertGroup(CachedGroup{GroupKey: victimKey, DisplayName: "Victim", CanonicalName: "victim"}); err != nil {
		t.Fatalf("seed victim group: %v", err)
	}
	if err := srv.store.UpsertEvent(CachedEvent{
		GroupKey: victimKey,
		EventID:  victimEventID,
		Title:    "Victim Event",
	}); err != nil {
		t.Fatalf("seed victim event: %v", err)
	}
	// Seed a single RSVP on the victim's event so the CSV path has
	// something to (not) leak.
	if err := srv.store.CreateRsvp(RSVPRecord{
		GroupKey:  victimKey,
		EventID:   victimEventID,
		UserEmail: "victim-attendee@example.com",
		UserName:  "Victim Attendee",
		Token:     "victim-rsvp-token",
		Confirmed: true,
	}); err != nil {
		t.Fatalf("seed victim RSVP: %v", err)
	}
	return attackerKey, victimKey, victimEventID
}

// ---- C-3: cross-group event_id IDOR on dashboard write endpoints ----

// TestC3_CheckIn_RejectsCrossGroupEvent is the C-3 fix. Before the
// fix, an organizer of group A could check in any RSVP at any
// event in any group by supplying the event_id in the form.
func TestC3_CheckIn_RejectsCrossGroupEvent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	attackerKey, _, victimEventID := seedTwoGroupsWithOneEvent(t, srv)

	form := "event_id=" + victimEventID + "&email=victim-attendee%40example.com&csrf_token=fake"
	req := authedRequest(t, srv, "POST", "/dashboard/events/"+attackerKey+"/checkin", form, attackerKey)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("C-3 REGRESSION: cross-group checkin should be 404, got %d "+
			"(attacker %s checked in RSVP on event %s)", w.Code, attackerKey, victimEventID)
	}
	// The RSVP must still be un-checked-in.
	rsvps, err := srv.store.ListRsvpsForEvent("victim-group-key", victimEventID)
	if err != nil {
		t.Fatalf("ListRsvpsForEvent: %v", err)
	}
	if len(rsvps) != 1 {
		t.Fatalf("expected 1 RSVP, got %d", len(rsvps))
	}
	if rsvps[0].Attended {
		t.Fatal("C-3 REGRESSION: cross-group checkin succeeded — the RSVP was marked attended")
	}
}

// TestC3_CancelEvent_RejectsCrossGroupEvent is the C-3 fix for
// handleCancelEvent. An organizer of group A used to be able to
// cancel any event in any group.
func TestC3_CancelEvent_RejectsCrossGroupEvent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	attackerKey, _, victimEventID := seedTwoGroupsWithOneEvent(t, srv)

	// handleCancelEvent takes event_id from the URL path, not the
	// form, so the form is empty. Pass CSRF via header.
	req := authedRequest(t, srv, "POST",
		"/dashboard/events/"+victimEventID+"/cancel", "", attackerKey)
	req.Header.Set("X-CSRF-Token", "fake")
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("C-3 REGRESSION: cross-group cancel should be 404, got %d", w.Code)
	}
	// The victim's event must still be non-cancelled in BOTH stores.
	if e, ok := srv.product.Store().GetEvent(victimEventID); ok && e.Cancelled {
		t.Fatal("C-3 REGRESSION: cross-group cancel succeeded in product store")
	}
	if ce, err := srv.store.GetEvent("victim-group-key", victimEventID); err == nil && ce.Cancelled {
		t.Fatal("C-3 REGRESSION: cross-group cancel succeeded in SQLite cache")
	}
}

// TestC3_AttendeesCSV_RejectsCrossGroupEvent is the C-3 fix for
// handleAttendeesCSV. The CSV path is the worst leak — it dumps
// the attendee list (names + emails + check-in status) verbatim.
// Before the fix, this fired for any event_id the attacker could
// supply, returning the victim's full attendee list.
func TestC3_AttendeesCSV_RejectsCrossGroupEvent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	attackerKey, _, victimEventID := seedTwoGroupsWithOneEvent(t, srv)

	req := authedRequest(t, srv, "GET",
		"/dashboard/events/"+victimEventID+"/attendees.csv", "", attackerKey)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("C-3 REGRESSION: cross-group CSV should be 404, got %d (body=%q)",
			w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "victim-attendee@example.com") {
		t.Fatalf("C-3 REGRESSION: victim's attendee email leaked into response. body=%q",
			w.Body.String()[:clip(200, len(w.Body.String()))])
	}
}

// TestC3_SameGroup_StillWorks is the positive control. The
// organizer of a group can still act on their own group's events.
func TestC3_SameGroup_StillWorks(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, victimKey, victimEventID := seedTwoGroupsWithOneEvent(t, srv)

	// Same group — should succeed.
	form := "event_id=" + victimEventID + "&email=victim-attendee%40example.com&csrf_token=fake"
	req := authedRequest(t, srv, "POST",
		"/dashboard/events/"+victimKey+"/checkin", form, victimKey)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("C-3 over-correction: same-group checkin now 404s, got %d", w.Code)
	}
	// The RSVP should be marked attended.
	rsvps, _ := srv.store.ListRsvpsForEvent(victimKey, victimEventID)
	if len(rsvps) != 1 || !rsvps[0].Attended {
		t.Fatalf("C-3 over-correction: same-group checkin didn't mark the RSVP attended "+
			"(status=%d, attended=%v)", w.Code, len(rsvps) > 0 && rsvps[0].Attended)
	}
}

// TestEventBelongsToGroup is the unit test for the helper that
// backs the C-3 fix.
func TestEventBelongsToGroup(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	attackerKey, victimKey, victimEventID := seedTwoGroupsWithOneEvent(t, srv)

	tests := []struct {
		name     string
		groupKey string
		eventID  string
		want     bool
	}{
		{"own group + own event", victimKey, victimEventID, true},
		{"attacker group + victim event", attackerKey, victimEventID, false},
		{"empty groupKey", "", victimEventID, false},
		{"empty eventID", victimKey, "", false},
		{"nonexistent event", victimKey, "no-such-event", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.eventBelongsToGroup(httptest.NewRequest("GET", "/", nil), tt.groupKey, tt.eventID)
			if got != tt.want {
				t.Fatalf("eventBelongsToGroup(%q, %q) = %v, want %v",
					tt.groupKey, tt.eventID, got, tt.want)
			}
		})
	}
}

// ---- H-4: SMTP CRLF injection in To: header ----

// TestSMTPSender_RejectsCRLFInTo is the H-4 regression test. The
// attack: caller passes a `to` value with embedded \r\n, the SMTP
// wire format picks up an injected Bcc: header. Reject any value
// that contains CR or LF.
func TestSMTPSender_RejectsCRLFInTo(t *testing.T) {
	s := emailpkg.NewSMTPSender(emailpkg.SMTPConfig{Host: "smtp.example.com", From: "noreply@example.com"})

	attacks := []string{
		"victim@example.com\r\nBcc: attacker@evil.com",
		"victim@example.com\nBcc: attacker@evil.com",
		"victim@example.com\r\nSubject: pwned",
		"victim@example.com\r\n\r\nnew body",
	}
	for _, atk := range attacks {
		err := s.Send(context.Background(), atk, "subject", "body")
		if err == nil {
			t.Errorf("H-4 REGRESSION: SMTPSender accepted CRLF in To: %q", atk)
		}
		if !strings.Contains(err.Error(), "CR/LF") {
			t.Errorf("H-4: error message should mention CR/LF, got %v", err)
		}
	}
}

// TestSMTPSender_RejectsControlChars covers the other injection
// vectors — NUL bytes and other non-printable ASCII control chars
// in the From / To / Subject path.
func TestSMTPSender_RejectsControlChars(t *testing.T) {
	s := emailpkg.NewSMTPSender(emailpkg.SMTPConfig{Host: "smtp.example.com", From: "noreply@example.com"})

	// Each entry: (label, to, subject). body is fixed.
	attacks := []struct {
		name    string
		to      string
		subject string
	}{
		{"NUL in To", "victim@example.com\x00Bcc: x", "ok"},
		{"NUL in Subject", "ok@example.com", "evil\x00subject"},
		{"DEL in To", "victim@example.com\x7f", "ok"},
		{"BEL in Subject", "ok@example.com", "evil\x07subject"},
		{"bare CR in Subject", "ok@example.com", "evil\rsubject"},
		{"bare LF in Subject", "ok@example.com", "evil\nsubject"},
	}
	for _, atk := range attacks {
		t.Run(atk.name, func(t *testing.T) {
			err := s.Send(context.Background(), atk.to, atk.subject, "body")
			if err == nil {
				t.Errorf("H-4: SMTPSender accepted %s in %q/%q", atk.name, atk.to, atk.subject)
			}
		})
	}
}

// TestSMTPSender_AcceptsNormalAddresses is the positive control.
func TestSMTPSender_AcceptsNormalAddresses(t *testing.T) {
	s := emailpkg.NewSMTPSender(emailpkg.SMTPConfig{Host: "smtp.example.com", From: "noreply@example.com"})

	// We can't actually reach an SMTP server from the test, but
	// ValidateHeaderValue runs BEFORE the network call. The test
	// asserts that the header-validation step is bypassed (i.e. the
	// error from smtp.SendMail connection refused does NOT mention
	// CR/LF or control char).
	err := s.Send(context.Background(), "alice@example.com", "Your event is tomorrow", "Hi Alice — see you there.")
	if err != nil {
		if strings.Contains(err.Error(), "CR/LF") || strings.Contains(err.Error(), "control char") {
			t.Fatalf("H-4 over-correction: normal email rejected by header check: %v", err)
		}
		// A connection error is expected and acceptable.
	}
}

// TestValidateHeaderValue is the unit test for the helper. The
// helper lives in the email package; we re-export it for testing
// via emailpkg.ValidateHeaderValue. If that name ever changes,
// update this import path.
func TestValidateHeaderValue(t *testing.T) {
	tests := []struct {
		name    string
		v       string
		wantErr bool
	}{
		{"normal email", "alice@example.com", false},
		{"empty", "", true},
		{"CR", "alice@example.com\rBcc: x", true},
		{"LF", "alice@example.com\nBcc: x", true},
		{"CRLF", "alice@example.com\r\nBcc: x", true},
		{"NUL", "alice@example.com\x00", true},
		{"DEL", "alice@example.com\x7f", true},
		{"body-newline-ok-but-not-control", "alice@example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := emailpkg.ValidateHeaderValue("test", tt.v)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ---- M-5: POST body size cap ----

// TestPOSTBodySizeCap_ReturnsError is the M-5 regression test.
// The csrfMiddleware installs a MaxBytesReader on every POST body.
// A request with a body larger than the cap should be rejected.
func TestPOSTBodySizeCap_Rejects(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Build a body that's larger than maxPOSTBodyBytes (64 KiB).
	// The form is x-www-form-urlencoded so we need valid encoding:
	// "csrf_token=fake&junk=" + a long string.
	huge := bytes.Repeat([]byte("A"), maxPOSTBodyBytes+1024)
	body := bytes.NewReader(append([]byte("csrf_token=fake&junk="), huge...))
	req := httptest.NewRequest("POST", "/my-rsvps/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})

	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// MaxBytesReader returns 413 Request Entity Too Large when the
	// handler tries to read past the cap. The handler reads the
	// body via ParseForm → FormValue, so the first overflow
	// surfaces as 413. We also accept 400 (some clients don't set
	// Content-Length) or 500 (handler not exercising ParseForm).
	// What's NOT acceptable: 200 OK that processed the giant body.
	if w.Code == http.StatusOK {
		t.Fatalf("M-5 REGRESSION: %d KiB POST body was accepted (status=%d, body=%q)",
			len(huge)/1024, w.Code, w.Body.String()[:clip(200, len(w.Body.String()))])
	}
	if w.Code < 400 || w.Code > 599 {
		t.Fatalf("M-5: expected 4xx/5xx, got %d", w.Code)
	}
}

// TestPOSTBodySizeCap_AllowsSmallBodies is the positive control.
func TestPOSTBodySizeCap_AllowsSmallBodies(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	body := strings.NewReader("email=alice%40example.com&csrf_token=fake")
	req := httptest.NewRequest("POST", "/my-rsvps/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})

	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// 200 OK is the "check your email" page.
	if w.Code != http.StatusOK {
		t.Fatalf("M-5 over-correction: small POST body rejected with %d (body=%q)",
			w.Code, w.Body.String()[:clip(200, len(w.Body.String()))])
	}
}

// clip is a tiny helper that returns min(n, max) for use in
// [:clip(...)] slicing. Lives in this test file because web_test.go
// already defines its own `min` for anti-dox tests; we avoid a
// package-level collision.
func clip(n, max int) int {
	if n > max {
		return max
	}
	return n
}
