// SPDX-License-Identifier: AGPL-3.0
//
// Regression tests for the 2026-07-06 security-audit hardening pass.
//
// Pinned audit findings (AUDIT-2026-07-06):
//   - C-4: organizer token logged in plaintext at group creation
//   - C-5: session and CSRF cookies set Secure: false
//   - H-1: CSRF cookie not HttpOnly
//   - H-6: no HSTS header
//   - L-6: CSP does not include frame-ancestors
//
// Each test below is a fail-before / pass-after regression. If any
// of these fail after a refactor, treat that as a security regression.

package web

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// withInsecureCookies sets FEDMEETUP_INSECURE_COOKIES=1 around fn and
// resets the env after. Used to exercise the dev-mode code path.
func withInsecureCookies(t *testing.T, fn func()) {
	t.Helper()
	prev := os.Getenv("FEDMEETUP_INSECURE_COOKIES")
	t.Cleanup(func() { os.Setenv("FEDMEETUP_INSECURE_COOKIES", prev) })
	if err := os.Setenv("FEDMEETUP_INSECURE_COOKIES", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	fn()
}

// findCookie returns the first cookie in resp.Cookies() matching name.
func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ---- H-6: HSTS only on TLS / when forceSecureCookies is true ----

func TestSecurityHeaders_HSTS_AbsentOverPlainHTTP(t *testing.T) {
	// Default config: prod-like (no dev override, no r.TLS).
	// HSTS must NOT be set on plain HTTP — otherwise a dev server
	// pinned HSTS on the origin and broke local development for two
	// years.
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS must not be emitted over plain HTTP, got %q", got)
	}
}

func TestSecurityHeaders_HSTS_PresentWhenInsecureCookiesDisabled(t *testing.T) {
	// HSTS also fires when an operator forces secure-cookie mode
	// without TLS (e.g. a TLS-terminating proxy in front that the
	// request doesn't know about — same `forceSecureCookies` knob).
	srv, cleanup := newTestServer(t)
	defer cleanup()
	srv.forceSecureCookies = true // simulate "always-on secure" config

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	got := w.Header().Get("Strict-Transport-Security")
	if got == "" {
		t.Fatal("HSTS should be set when forceSecureCookies=true")
	}
	if !strings.Contains(got, "max-age=63072000") {
		t.Fatalf("HSTS should have 2-year max-age, got %q", got)
	}
}

// ---- C-5 + H-1: session and CSRF cookie hardening in prod mode ----

// triggerCookieIssue makes a request that causes csrfMiddleware to
// emit a Set-Cookie for the CSRF cookie. We then inspect the
// Set-Cookie header to assert the hardening flags.
func triggerCookieIssue(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	return w
}

func TestCSRFCookie_SecureAndHttpOnly_ProductionMode(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	w := triggerCookieIssue(t, srv)
	c := findCookie(w.Result(), CSRFCookieName)
	if c == nil {
		t.Fatal("CSRF cookie should be set on first request")
	}
	if !c.HttpOnly {
		t.Fatalf("C-5/H-1 REGRESSION: CSRF cookie HttpOnly=false, "+
			"an XSS can read the token via document.cookie (got HttpOnly=%v)", c.HttpOnly)
	}
	if !c.Secure {
		t.Fatalf("C-5 REGRESSION: CSRF cookie Secure=false, "+
			"any HTTP leg leaks the token (got Secure=%v)", c.Secure)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("CSRF cookie SameSite should be Lax, got %v", c.SameSite)
	}
}

func TestCSRFCookie_InsecureInDevMode(t *testing.T) {
	// Dev mode: FEDMEETUP_INSECURE_COOKIES=1 → Secure: false,
	// HttpOnly still true (that's defense in depth, not a transport
	// concern). Confirms the dev opt-out works.
	withInsecureCookies(t, func() {
		srv, cleanup := newTestServer(t)
		defer cleanup()

		w := triggerCookieIssue(t, srv)
		c := findCookie(w.Result(), CSRFCookieName)
		if c == nil {
			t.Fatal("CSRF cookie should be set on first request")
		}
		if c.Secure {
			t.Fatal("dev mode: CSRF cookie should NOT be Secure")
		}
		if !c.HttpOnly {
			t.Fatal("CSRF cookie should still be HttpOnly even in dev")
		}
	})
}

// ---- C-5: session cookie hardening ----

// TestSessionCookie_Secure is tested by minting an organizer token
// in the product store, then logging in via the public /dashboard/login
// endpoint. The handler sets the session cookie via s.setSessionCookie.
func TestSessionCookie_SecureInProduction(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	const groupKey = "sess-group"
	const orgToken = "test-org-token-sess"
	srv.product.Store().PutGroup(&pb.Group{GroupId: groupKey, DisplayName: "S"})
	srv.product.Store().PutOrganizerToken(orgToken, groupKey)

	form := strings.NewReader("token=" + orgToken + "&csrf_token=fake")
	req := httptest.NewRequest("POST", "/dashboard/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after login, got %d (body=%q)", w.Code, w.Body.String())
	}
	c := findCookie(w.Result(), SessionCookieName)
	if c == nil {
		t.Fatal("session cookie should be set after login")
	}
	if !c.Secure {
		t.Fatalf("C-5 REGRESSION: session cookie Secure=false, "+
			"any HTTP leg leaks the dashboard session (got Secure=%v)", c.Secure)
	}
	if !c.HttpOnly {
		t.Fatal("session cookie should be HttpOnly (it already was; pinning it)")
	}
}

func TestSessionCookie_InsecureInDevMode(t *testing.T) {
	withInsecureCookies(t, func() {
		srv, cleanup := newTestServer(t)
		defer cleanup()

		const groupKey = "sess-group-dev"
		const orgToken = "test-org-token-dev"
		srv.product.Store().PutGroup(&pb.Group{GroupId: groupKey, DisplayName: "D"})
		srv.product.Store().PutOrganizerToken(orgToken, groupKey)

		form := strings.NewReader("token=" + orgToken + "&csrf_token=fake")
		req := httptest.NewRequest("POST", "/dashboard/login", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
		w := httptest.NewRecorder()
		srv.Routes().ServeHTTP(w, req)

		c := findCookie(w.Result(), SessionCookieName)
		if c == nil {
			t.Fatal("session cookie should be set after login")
		}
		if c.Secure {
			t.Fatal("dev mode: session cookie should NOT be Secure")
		}
		if !c.HttpOnly {
			t.Fatal("session cookie should be HttpOnly even in dev mode")
		}
	})
}

// ---- C-4: organizer token must NOT appear in logs ----

// captureLog redirects log output to a buffer for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})
	fn()
	return buf.String()
}

// TestOrganizerToken_NotLogged is the C-4 regression test. It
// invokes handleCreateGroup directly (the HTTP route is not yet
// registered — that's a separate bug tracked outside this audit)
// and asserts that the plaintext organizer token (32-hex-char
// shape) never appears in any log line. The fingerprint
// (8 hex chars) IS allowed and is asserted below.
func TestOrganizerToken_NotLogged(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	logged := captureLog(t, func() {
		form := strings.NewReader(
			"display_name=C4LogTestGroup&description=d&" +
				"organizer_name=Alice&organizer_email=alice%40c4-test.example.com&" +
				"csrf_token=fake",
		)
		req := httptest.NewRequest("POST", "/dashboard/create", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
		w := httptest.NewRecorder()
		// Call the handler directly. (The HTTP route is not yet
		// registered — separate issue. The handler is what we
		// want to pin behavior for.)
		srv.handleCreateGroup(w, req)
	})

	// 1. C-4 specifically: the line must NOT contain the old
	//    "organizer token for" marker. That log statement is what
	//    leaked the credential before the fix. Any line that
	//    contains it is a regression.
	if strings.Contains(logged, "organizer token for") {
		t.Errorf("C-4 REGRESSION: legacy log marker \"organizer token for\" is "+
			"present, the plaintext token is being logged. Captured log:\n%s", logged)
	}

	// 2. The "group created" log line should fire (proving the path
	//    ran). Look for the sentinel display name.
	foundCreateLine := false
	for _, line := range strings.Split(logged, "\n") {
		if !strings.Contains(line, "C4LogTestGroup") {
			continue
		}
		foundCreateLine = true
		// 3. The new log line MUST include the fingerprint marker.
		if !strings.Contains(line, "token fp=") {
			t.Errorf("C-4 REGRESSION: group-created log line has no fingerprint, "+
				"token may have been logged: %q", line)
		}
		// 4. The fingerprint itself should be short (8 hex chars),
		//    not the full token (32 hex chars). The fingerprint
		//    appears after "token fp=" in the same log line.
		i := strings.Index(line, "token fp=")
		if i >= 0 {
			fp := line[i+len("token fp="):]
			fp = strings.TrimRight(fp, " 	\r\n")
			if len(fp) > 16 {
				t.Errorf("C-4 REGRESSION: token-fingerprint in log is %d chars, "+
					"suspicious for a 32-bit hash. Log line: %q", len(fp), line)
			}
		}
		// 5. The line must NOT contain a bare 32+ hex-char substring
		//    that isn't followed by "(C4LogTestGroup)" or "(token fp=...)"
		//    — those are the group_key (acceptable, not a credential)
		//    and fingerprint (acceptable) respectively. Any OTHER long
		//    hex string in the line is suspicious.
		// We achieve this with a simple check: the only long hex
		// substring allowed is the group_key, which is positioned
		// immediately before " (C4LogTestGroup)".
		for _, word := range strings.Fields(line) {
			if isAllHex(word) && len(word) >= 32 {
				// The group_key pattern: "<hex> (C4LogTestGroup)"
				// is fine. Anything else is a regression.
				allowedContext := strings.Contains(line, word+" (C4LogTestGroup)") ||
					strings.Contains(line, word+" \u2014") // em-dash separator before organizer name
				if !allowedContext {
					t.Errorf("C-4 REGRESSION: log line contains an unexplained "+
						"%d-char hex string (likely the leaked organizer token). "+
						"Line: %q", len(word), line)
				}
			}
		}
	}
	if !foundCreateLine {
		t.Fatalf("log capture didn't see the create handler fire; "+
			"can't run C-4 regression. Logged: %s", logged)
	}
}

// isAllHex returns true if s is non-empty and every rune is [0-9a-fA-F].
func isAllHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// ---- L-6: CSP includes frame-ancestors 'none' ----

func TestCSP_FrameAncestorsNone(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("L-6 REGRESSION: CSP missing frame-ancestors 'none', got: %s", csp)
	}
}

// ---- Sanity: organizerTokenFingerprint is non-reversible + stable ----

func TestOrganizerTokenFingerprint(t *testing.T) {
	a := organizerTokenFingerprint("0123456789abcdef0123456789abcdef")
	b := organizerTokenFingerprint("0123456789abcdef0123456789abcdef")
	c := organizerTokenFingerprint("ffffffffffffffffffffffffffffffff")
	if a != b {
		t.Fatalf("fingerprint should be deterministic: %s vs %s", a, b)
	}
	if a == c {
		t.Fatal("fingerprint should differentiate distinct inputs")
	}
	if len(a) != 8 {
		t.Fatalf("fingerprint should be 8 hex chars, got %d (%q)", len(a), a)
	}
	// C-4 contract: the fingerprint must NOT equal the input.
	if a == "0123456789abcdef0123456789abcdef" {
		t.Fatal("fingerprint echoed its input — non-reversibility broken")
	}
}

// Compile-time guard: ctx import is used by callers of captureSender
// in anti_dox_test.go; the test file in this package doesn't need
// it, but listing the import keeps gofmt happy if a future test
// adds an email-sender assertion.
var _ = context.Background
var _ = time.Now
