// SPDX-License-Identifier: AGPL-3.0

//go:build e2e

// End-to-end integration test for the federated-meetup system.
//
// This test builds the real fedmeetup binary, starts it as a subprocess
// with test configuration, and exercises every product surface:
//
//   - Health and identity endpoints
//   - Web UI (home, group, event pages)
//   - ICS calendar export
//   - Federation discovery (.well-known/federation, llms.txt, openapi.json)
//   - ActivityPub (WebFinger, actor, outbox)
//   - RSVP flow (submit, magic-link confirm, my-rsvps)
//   - Organizer dashboard (login, create event)
//   - Checkout page (for ticketed events)
//   - MCP server (initialize handshake)
//
// Run with: go test -tags e2e ./test/ -v -timeout 60s
package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Server configuration constants for the e2e test.
const (
	e2eAddr     = "127.0.0.1:18091"
	e2eBaseURL  = "http://localhost:18091"
	e2eDBPath   = "/tmp/fedmeetup-e2e.db"
	e2eBinary   = "/tmp/fedmeetup-e2e"
	e2eGroupKey = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	e2eArea     = "Las Vegas, NV"
	// Seed data constants
	e2eGroupCanonical = "vegas-programmers"
	e2eEventID        = "evt-go-night"
	// Demo organizer token (from seed.go)
	e2eOrganizerToken = "demo-organizer-token"
	// Test RSVP email
	e2eRSVPEmail = "alice@example.com"
	e2eRSVPName  = "Alice"
)

// TestE2E_FullProductFlow is the single comprehensive end-to-end test.
// It exercises all product surfaces in sequence against a real binary.
func TestE2E_FullProductFlow(t *testing.T) {
	// ---- Build the binary ----
	repoRoot := findRepoRoot(t)
	buildBinary(t, repoRoot)
	t.Cleanup(func() {
		_ = os.Remove(e2eBinary)
	})

	// ---- Start the server ----
	srv := startServer(t)
	t.Cleanup(func() {
		srv.cleanup(t)
	})

	// Create an HTTP client with cookie jar (for session/CSRF cookies).
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		// Don't follow redirects automatically — we want to inspect them.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// ---- Test all endpoints ----
	t.Run("healthz", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/healthz", 200)
		if !strings.Contains(body, "ok") {
			t.Fatalf("healthz should return 'ok', got: %q", body)
		}
	})

	t.Run("identity", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/identity", 200)
		var id struct {
			GroupKey  string `json:"group_key"`
			Name      string `json:"name"`
			Threshold int    `json:"threshold"`
		}
		if err := json.Unmarshal([]byte(body), &id); err != nil {
			t.Fatalf("identity: parse JSON: %v\nbody: %s", err, body)
		}
		if !strings.HasPrefix(id.GroupKey, "0x") {
			t.Fatalf("identity: group_key should start with 0x, got: %s", id.GroupKey)
		}
		if id.GroupKey != e2eGroupKey {
			t.Fatalf("identity: group_key mismatch: want %s, got %s", e2eGroupKey, id.GroupKey)
		}
		if id.Name == "" {
			t.Fatal("identity: name should not be empty")
		}
	})

	t.Run("home_page", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/", 200)
		if !strings.Contains(body, "Vegas Programmers") {
			t.Fatal("home page should contain 'Vegas Programmers'")
		}
		if !strings.Contains(body, "Go Night") {
			t.Fatal("home page should contain 'Go Night' event")
		}
	})

	t.Run("group_page", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/groups/"+e2eGroupCanonical, 200)
		if !strings.Contains(body, "Vegas Programmers") {
			t.Fatal("group page should contain 'Vegas Programmers'")
		}
		if !strings.Contains(body, "Go Night") {
			t.Fatal("group page should contain 'Go Night' event")
		}
	})

	t.Run("event_page", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+e2eEventID, 200)
		if !strings.Contains(body, "Go Night") {
			t.Fatal("event page should contain 'Go Night' title")
		}
		if !strings.Contains(body, "application/ld+json") {
			t.Fatal("event page should contain schema.org JSON-LD")
		}
		if !strings.Contains(body, "Event") {
			t.Fatal("JSON-LD should contain @type Event")
		}
	})

	t.Run("event_ics", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+e2eEventID+"/calendar.ics", 200)
		if !strings.Contains(body, "BEGIN:VCALENDAR") {
			t.Fatal("ICS should contain BEGIN:VCALENDAR")
		}
		if !strings.Contains(body, "BEGIN:VEVENT") {
			t.Fatal("ICS should contain BEGIN:VEVENT")
		}
		if !strings.Contains(body, "Go Night") {
			t.Fatal("ICS should contain event title 'Go Night'")
		}
		if !strings.Contains(body, "END:VCALENDAR") {
			t.Fatal("ICS should contain END:VCALENDAR")
		}
	})

	t.Run("federation_discovery", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/.well-known/federation", 200)
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("federation: parse JSON: %v\nbody: %s", err, body)
		}
		if doc["protocol"] != "federated-meetup/v1" {
			t.Fatalf("federation: protocol should be 'federated-meetup/v1', got: %v", doc["protocol"])
		}
		host, ok := doc["host"].(map[string]interface{})
		if !ok {
			t.Fatal("federation: missing 'host' object")
		}
		if host["geographic_area"] != e2eArea {
			t.Fatalf("federation: area should be %q, got: %v", e2eArea, host["geographic_area"])
		}
		if doc["mcp_endpoint"] != "/mcp" {
			t.Fatalf("federation: mcp_endpoint should be '/mcp', got: %v", doc["mcp_endpoint"])
		}
	})

	t.Run("llms_txt", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/llms.txt", 200)
		if !strings.Contains(body, "## Groups") {
			t.Fatal("llms.txt should contain '## Groups' section")
		}
		if !strings.Contains(body, "## API") {
			t.Fatal("llms.txt should contain '## API' section")
		}
		if !strings.Contains(body, "MCP server: POST /mcp") {
			t.Fatal("llms.txt should mention MCP server")
		}
	})

	t.Run("openapi_json", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/openapi.json", 200)
		var spec map[string]interface{}
		if err := json.Unmarshal([]byte(body), &spec); err != nil {
			t.Fatalf("openapi: parse JSON: %v\nbody: %s", err, body)
		}
		if spec["openapi"] != "3.1.0" {
			t.Fatalf("openapi: version should be 3.1.0, got: %v", spec["openapi"])
		}
		paths, ok := spec["paths"].(map[string]interface{})
		if !ok {
			t.Fatal("openapi: missing 'paths' object")
		}
		if _, ok := paths["/list-groups"]; !ok {
			t.Fatal("openapi: should have /list-groups path")
		}
	})

	t.Run("webfinger", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/.well-known/webfinger?resource=acct:"+e2eGroupCanonical+"@localhost", 200)
		var wf map[string]interface{}
		if err := json.Unmarshal([]byte(body), &wf); err != nil {
			t.Fatalf("webfinger: parse JSON: %v\nbody: %s", err, body)
		}
		if wf["subject"] != "acct:"+e2eGroupCanonical+"@localhost" {
			t.Fatalf("webfinger: subject mismatch: got %v", wf["subject"])
		}
		links, ok := wf["links"].([]interface{})
		if !ok || len(links) == 0 {
			t.Fatal("webfinger: should have links")
		}
	})

	t.Run("activitypub_actor", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/ap/actor/"+e2eGroupCanonical, 200)
		var actor map[string]interface{}
		if err := json.Unmarshal([]byte(body), &actor); err != nil {
			t.Fatalf("ap actor: parse JSON: %v\nbody: %s", err, body)
		}
		if actor["type"] != "Group" {
			t.Fatalf("ap actor: type should be 'Group', got: %v", actor["type"])
		}
		if actor["preferredUsername"] != e2eGroupCanonical {
			t.Fatalf("ap actor: preferredUsername should be %q, got: %v", e2eGroupCanonical, actor["preferredUsername"])
		}
	})

	t.Run("activitypub_outbox", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/ap/outbox/"+e2eGroupCanonical, 200)
		var coll map[string]interface{}
		if err := json.Unmarshal([]byte(body), &coll); err != nil {
			t.Fatalf("ap outbox: parse JSON: %v\nbody: %s", err, body)
		}
		if coll["type"] != "OrderedCollection" {
			t.Fatalf("ap outbox: type should be 'OrderedCollection', got: %v", coll["type"])
		}
		items, ok := coll["orderedItems"].([]interface{})
		if !ok {
			t.Fatal("ap outbox: should have orderedItems array")
		}
		if len(items) == 0 {
			t.Fatal("ap outbox: should have at least one event activity")
		}
	})

	// ---- RSVP flow ----
	var rsvpToken string

	t.Run("rsvp_submit", func(t *testing.T) {
		// First GET the event page to obtain a CSRF token cookie.
		httpGet(t, client, e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+e2eEventID, 200)

		// Extract CSRF token from the cookie jar.
		csrf := getCSRFToken(t, client, e2eBaseURL)

		form := url.Values{}
		form.Set("email", e2eRSVPEmail)
		form.Set("name", e2eRSVPName)
		form.Set("csrf_token", csrf)

		req, err := http.NewRequest("POST", e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+e2eEventID+"/rsvp", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("rsvp: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("rsvp: do request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("rsvp: expected 200, got %d\nbody: %s", resp.StatusCode, body)
		}

		bodyStr := string(body)
		if !strings.Contains(bodyStr, e2eRSVPEmail) && !strings.Contains(bodyStr, "check") && !strings.Contains(bodyStr, "magic") && !strings.Contains(bodyStr, "email") {
			// The RSVP fragment should contain a success message.
			// The exact text depends on the template, but it should mention the email or a confirmation.
			t.Logf("rsvp response body: %s", bodyStr)
		}

		// Extract the token from the SQLite database.
		rsvpToken = extractRSVPTokenFromDB(t, e2eDBPath, e2eRSVPEmail)
		if rsvpToken == "" {
			t.Fatal("rsvp: failed to extract RSVP token from database")
		}
		t.Logf("rsvp: extracted token %s for email %s", rsvpToken, e2eRSVPEmail)
	})

	t.Run("rsvp_confirm_page", func(t *testing.T) {
		if rsvpToken == "" {
			t.Skip("no RSVP token available")
		}
		body := httpGet(t, client, e2eBaseURL+"/rsvp/"+rsvpToken, 200)
		if !strings.Contains(body, "Go Night") {
			t.Fatalf("rsvp confirm page should contain 'Go Night', got: %s", body)
		}
		if !strings.Contains(body, e2eRSVPEmail) {
			t.Fatalf("rsvp confirm page should contain email %q", e2eRSVPEmail)
		}
	})

	t.Run("rsvp_confirm_post", func(t *testing.T) {
		if rsvpToken == "" {
			t.Skip("no RSVP token available")
		}
		// Get CSRF token from the confirm page (already fetched above).
		csrf := getCSRFToken(t, client, e2eBaseURL)

		form := url.Values{}
		form.Set("csrf_token", csrf)

		req, err := http.NewRequest("POST", e2eBaseURL+"/rsvp/"+rsvpToken+"/confirm", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("rsvp confirm: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("rsvp confirm: do request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("rsvp confirm: expected 200, got %d\nbody: %s", resp.StatusCode, body)
		}
		// The confirmed fragment should contain a success message.
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "confirmed") && !strings.Contains(bodyStr, "Confirmed") && !strings.Contains(bodyStr, "You're in") && !strings.Contains(bodyStr, "success") {
			t.Logf("rsvp confirm response (may still be OK): %s", bodyStr)
		}

		// Verify in the database that the RSVP is confirmed.
		if !isRSVPConfirmedInDB(t, e2eDBPath, rsvpToken) {
			t.Fatal("rsvp confirm: RSVP should be marked as confirmed in the database")
		}
	})

	t.Run("my_rsvps", func(t *testing.T) {
		if rsvpToken == "" {
			t.Skip("no RSVP token available")
		}
		body := httpGet(t, client, e2eBaseURL+"/my-rsvps?email="+url.QueryEscape(e2eRSVPEmail), 200)
		if !strings.Contains(body, e2eRSVPEmail) {
			t.Fatal("my-rsvps page should contain the email")
		}
		if !strings.Contains(body, "Go Night") {
			t.Fatal("my-rsvps page should contain the event title 'Go Night'")
		}
	})

	// ---- Dashboard flow ----
	var sessionCookie string

	t.Run("dashboard_login_page", func(t *testing.T) {
		body := httpGet(t, client, e2eBaseURL+"/dashboard/login", 200)
		if !strings.Contains(body, "token") && !strings.Contains(body, "Token") && !strings.Contains(body, "login") {
			t.Fatal("dashboard login page should contain a login form")
		}
	})

	t.Run("dashboard_login", func(t *testing.T) {
		// GET the login page first to get a CSRF cookie.
		httpGet(t, client, e2eBaseURL+"/dashboard/login", 200)
		csrf := getCSRFToken(t, client, e2eBaseURL)

		form := url.Values{}
		form.Set("token", e2eOrganizerToken)
		form.Set("csrf_token", csrf)

		req, err := http.NewRequest("POST", e2eBaseURL+"/dashboard/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("dashboard login: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("dashboard login: do request: %v", err)
		}
		defer resp.Body.Close()

		// Should redirect to /dashboard (303 See Other).
		if resp.StatusCode != 303 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("dashboard login: expected 303 redirect, got %d\nbody: %s", resp.StatusCode, body)
		}
		loc := resp.Header.Get("Location")
		if loc != "/dashboard" {
			t.Fatalf("dashboard login: redirect should be to /dashboard, got: %s", loc)
		}

		// Extract session cookie.
		u, _ := url.Parse(e2eBaseURL)
		cookies := client.Jar.Cookies(u)
		for _, c := range cookies {
			if c.Name == "fedmeetup_session" {
				sessionCookie = c.Value
			}
		}
		if sessionCookie == "" {
			t.Fatal("dashboard login: session cookie should be set")
		}
	})

	t.Run("dashboard_view", func(t *testing.T) {
		if sessionCookie == "" {
			t.Skip("no session cookie available")
		}
		body := httpGet(t, client, e2eBaseURL+"/dashboard", 200)
		// Dashboard should contain event management controls.
		if !strings.Contains(body, "Dashboard") && !strings.Contains(body, "dashboard") {
			t.Fatal("dashboard page should contain 'Dashboard'")
		}
		// Should show existing seeded events.
		if !strings.Contains(body, "Go Night") {
			t.Fatal("dashboard should contain 'Go Night' event")
		}
	})

	// ---- Create a new event via dashboard ----
	var newEventID string

	t.Run("dashboard_create_event", func(t *testing.T) {
		if sessionCookie == "" {
			t.Skip("no session cookie available")
		}
		// Get CSRF token (already have it from login).
		csrf := getCSRFToken(t, client, e2eBaseURL)

		// Create a future event.
		startTime := time.Now().AddDate(0, 0, 30).Format("2006-01-02T15:04")
		form := url.Values{}
		form.Set("title", "E2E Test Event")
		form.Set("description", "Created by the end-to-end integration test")
		form.Set("starts_at", startTime)
		form.Set("location", "Test Location")
		form.Set("capacity", "25")
		form.Set("csrf_token", csrf)

		req, err := http.NewRequest("POST", e2eBaseURL+"/dashboard/events", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("create event: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create event: do request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("create event: expected 200, got %d\nbody: %s", resp.StatusCode, body)
		}

		bodyStr := string(body)
		if !strings.Contains(bodyStr, "E2E Test Event") && !strings.Contains(bodyStr, "Event created") && !strings.Contains(bodyStr, "created") {
			t.Fatalf("create event: response should contain success message, got: %s", bodyStr)
		}

		// Extract the new event ID from the database.
		newEventID = extractLatestEventIDFromDB(t, e2eDBPath, "E2E Test Event")
		if newEventID == "" {
			t.Fatal("create event: failed to extract new event ID from database")
		}
		t.Logf("created new event: %s", newEventID)
	})

	t.Run("new_event_page", func(t *testing.T) {
		if newEventID == "" {
			t.Skip("no new event ID available")
		}
		body := httpGet(t, client, e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+newEventID, 200)
		if !strings.Contains(body, "E2E Test Event") {
			t.Fatalf("new event page should contain 'E2E Test Event', got: %s", body)
		}
	})

	// ---- Checkout flow (for ticketed events) ----
	t.Run("checkout_flow", func(t *testing.T) {
		// The evt-go-night event has a ticket (tick-go-night, $25.00).
		// First, purchase a ticket to create an order.
		csrf := getCSRFToken(t, client, e2eBaseURL)

		form := url.Values{}
		form.Set("ticket_id", "tick-go-night")
		form.Set("email", e2eRSVPEmail)
		form.Set("csrf_token", csrf)

		req, err := http.NewRequest("POST", e2eBaseURL+"/events/"+e2eGroupCanonical+"/"+e2eEventID+"/purchase", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("purchase: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("purchase: do request: %v", err)
		}
		defer resp.Body.Close()

		// Should redirect to /checkout/{order_id} (303 See Other).
		if resp.StatusCode != 303 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("purchase: expected 303 redirect, got %d\nbody: %s", resp.StatusCode, body)
		}
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "/checkout/") {
			t.Fatalf("purchase: redirect should start with /checkout/, got: %s", loc)
		}
		orderID := strings.TrimPrefix(loc, "/checkout/")

		// Now GET the checkout page.
		body := httpGet(t, client, e2eBaseURL+loc, 200)
		if !strings.Contains(body, "Go Night") {
			t.Fatal("checkout page should contain 'Go Night' event title")
		}
		if !strings.Contains(body, orderID) {
			t.Fatalf("checkout page should contain order ID %q", orderID)
		}
		t.Logf("checkout: order %s verified", orderID)
	})

	// ---- MCP endpoint ----
	t.Run("mcp_initialize", func(t *testing.T) {
		// MCP requires Content-Type: application/json and Accept: application/json, text/event-stream.
		// The MCP streamable-http transport expects a JSON-RPC initialize request.
		initReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":   map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "e2e-test",
					"version": "1.0.0",
				},
			},
		}
		reqBody, _ := json.Marshal(initReq)

		req, err := http.NewRequest("POST", e2eBaseURL+"/mcp", bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("mcp: create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("mcp: do request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("mcp: expected 200, got %d\nbody: %s", resp.StatusCode, body)
		}

		bodyStr := string(body)

		// The MCP initialize response might be SSE (text/event-stream) or JSON.
		// Look for the protocol version or server info in the response.
		if !strings.Contains(bodyStr, "result") && !strings.Contains(bodyStr, "protocolVersion") {
			t.Fatalf("mcp: response should contain 'result' or 'protocolVersion', got: %s", bodyStr)
		}
		t.Logf("mcp: initialize response received (len=%d)", len(body))
	})
}

// ---- Helpers ----

// serverInstance holds the running server process and its output.
type serverInstance struct {
	cmd    *exec.Cmd
	output *bytes.Buffer
}

// cleanup kills the server process and removes the temp DB.
func (s *serverInstance) cleanup(t *testing.T) {
	t.Helper()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	_ = os.Remove(e2eDBPath)
	// Also remove WAL/SHM sidecar files.
	_ = os.Remove(e2eDBPath + "-wal")
	_ = os.Remove(e2eDBPath + "-shm")
}

// findRepoRoot walks up from the test directory to find go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

// buildBinary builds the fedmeetup binary and returns its path.
func buildBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", e2eBinary, "./cmd/fedmeetup")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(e2eBinary); err != nil {
		t.Fatalf("binary not found at %s: %v", e2eBinary, err)
	}
	t.Logf("built binary: %s", e2eBinary)
	return e2eBinary
}

// startServer starts the fedmeetup binary as a subprocess and waits for it to be healthy.
func startServer(t *testing.T) *serverInstance {
	t.Helper()

	// Remove any existing DB.
	_ = os.Remove(e2eDBPath)
	_ = os.Remove(e2eDBPath + "-wal")
	_ = os.Remove(e2eDBPath + "-shm")

	cmd := exec.Command(e2eBinary)
	cmd.Env = append(os.Environ(),
		"HOSTD_GROUP_KEY="+e2eGroupKey,
		"HOSTD_ADDR="+e2eAddr,
		"HOSTD_BASE_URL="+e2eBaseURL,
		"HOSTD_DB_PATH="+e2eDBPath,
		"HOSTD_AREA="+e2eArea,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	si := &serverInstance{cmd: cmd, output: &output}

	// Wait for healthz to return ok.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(e2eBaseURL + "/healthz")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && strings.Contains(string(body), "ok") {
				t.Logf("server is healthy (addr=%s)", e2eAddr)
				return si
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Server didn't start — print the output for debugging.
	t.Fatalf("server did not become healthy within 30s\n--- server output ---\n%s", output.String())
	return si
}

// httpGet performs a GET request and returns the response body as a string.
func httpGet(t *testing.T, client *http.Client, url string, wantStatus int) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: expected %d, got %d\nbody: %s", url, wantStatus, resp.StatusCode, body)
	}
	return string(body)
}

// getCSRFToken extracts the CSRF token from the cookie jar.
func getCSRFToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	cookies := client.Jar.Cookies(u)
	for _, c := range cookies {
		if c.Name == "fedmeetup_csrf" {
			return c.Value
		}
	}
	t.Fatal("no CSRF token cookie found — did you make a GET request first?")
	return ""
}

// extractRSVPTokenFromDB queries the SQLite database for the RSVP token.
func extractRSVPTokenFromDB(t *testing.T, dbPath, email string) string {
	t.Helper()
	return queryDB(t, dbPath, "SELECT token FROM rsvps WHERE user_email = ? ORDER BY created_at DESC LIMIT 1", email)
}

// isRSVPConfirmedInDB checks if the RSVP is marked as confirmed in the database.
func isRSVPConfirmedInDB(t *testing.T, dbPath, token string) bool {
	t.Helper()
	result := queryDB(t, dbPath, "SELECT confirmed FROM rsvps WHERE token = ?", token)
	return result == "1"
}

// extractLatestEventIDFromDB queries the SQLite database for the most recently created event.
func extractLatestEventIDFromDB(t *testing.T, dbPath, title string) string {
	t.Helper()
	return queryDB(t, dbPath, "SELECT event_id FROM events_cache WHERE title = ? ORDER BY cached_at DESC LIMIT 1", title)
}

// queryDB is a helper that runs a single-value SQL query against the SQLite database.
func queryDB(t *testing.T, dbPath, query string, args ...interface{}) string {
	t.Helper()

	// Open the DB in read-only mode with a short timeout.
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	defer db.Close()

	var result string
	err = db.QueryRow(query, args...).Scan(&result)
	if err != nil {
		// Return empty string if no rows found.
		return ""
	}
	return result
}