// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

// CSPHeader is the Content-Security-Policy for the web frontend.
// script-src 'self' allows vendored htmx.min.js and theme.js.
// style-src 'self' 'unsafe-inline' allows inline styles in event JSON-LD.
// connect-src 'self' allows HTMX requests to same origin.
const CSPHeader = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'"

// SessionCookieName is the cookie name for organizer sessions.
const SessionCookieName = "fedmeetup_session"

// CSRFCookieName is the cookie name for CSRF tokens (double-submit pattern).
const CSRFCookieName = "fedmeetup_csrf"

// CSRFHeaderName is the form field / header name for CSRF tokens.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFFormField is the form field name for CSRF tokens in HTML forms.
const CSRFFormField = "csrf_token"

// withMiddleware wraps the mux with security headers, CSRF protection,
// and request logging.
func (s *Server) withMiddleware(h http.Handler) http.Handler {
	return s.csrfMiddleware(s.securityHeaders(s.loggingMiddleware(h)))
}

// securityHeaders adds CSP and other security headers to all responses.
func (s *Server) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", CSPHeader)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request.
func (s *Server) loggingMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("web: %s %s %s %v", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

// csrfMiddleware implements the double-submit cookie pattern:
//  1. On every request, if no CSRF cookie exists, set one.
//  2. On POST/PUT/DELETE, verify that the form field "csrf_token" matches
//     the cookie value.
//  3. If they don't match, return 403.
func (s *Server) csrfMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ensure CSRF cookie exists
		cookie, err := r.Cookie(CSRFCookieName)
		if err != nil || cookie.Value == "" {
			token := generateCSRFToken()
			http.SetCookie(w, &http.Cookie{
				Name:     CSRFCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: false, // must be readable by JS if needed; we use form fields
				Secure:   false, // set to true behind TLS in production
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400 * 30, // 30 days
			})
			// Store in request context for handlers to use
			r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
		}

		// Only check CSRF on state-changing methods
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			if !s.validateCSRF(r) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`<div class="alert alert-error"><p class="font-medium">CSRF token validation failed. Please refresh the page and try again.</p></div>`))
				return
			}
		}

		h.ServeHTTP(w, r)
	})
}

// validateCSRF checks that the form field CSRF token matches the cookie.
func (s *Server) validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}

	// Try form field first (standard forms)
	_ = r.ParseForm()
	formToken := r.FormValue(CSRFFormField)
	if formToken != "" {
		return subtleEqual(formToken, cookie.Value)
	}

	// Try header (HTMX may send it as a header)
	headerToken := r.Header.Get(CSRFHeaderName)
	if headerToken != "" {
		return subtleEqual(headerToken, cookie.Value)
	}

	return false
}

// csrfTokenFromRequest returns the CSRF token from the cookie.
func csrfTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// generateCSRFToken generates a random 32-char hex token.
func generateCSRFToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return hex.EncodeToString(b)
}

// subtleEqual does a constant-time string comparison to prevent timing attacks.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

// isAuthenticated checks if the request has a valid organizer session cookie.
// Returns the group_key if authenticated.
func (s *Server) isAuthenticated(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return s.store.ValidateSession(cookie.Value)
}

// setSessionCookie sets the organizer session cookie.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // set to true behind TLS in production
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7, // 7 days
	})
}

// clearSessionCookie clears the organizer session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// sanitizePath removes any path traversal attempts.
func sanitizePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "//", "/")
	return s
}