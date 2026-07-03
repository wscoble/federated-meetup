// SPDX-License-Identifier: AGPL-3.0
//
// Tests for the MCP server and discovery endpoints.
//
// These tests exercise:
//   - MCP server construction and tool registration
//   - Discovery endpoints (/.well-known/federation, /llms.txt,
//     /openapi.json, /robots.txt) via HTTP handler
//   - RegisterEndpoints wiring (all endpoints on one ServeMux)
package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/host"
	"github.com/sscoble/federated-meetup/internal/mcp"
	"github.com/sscoble/federated-meetup/internal/types"
)

// testConfig returns a HostConfig suitable for tests.
func testConfig() mcp.HostConfig {
	return mcp.HostConfig{
		Name:           "Test Host",
		URL:            "https://test.example.com",
		Description:    "Test host for unit tests",
		GeographicArea: "Las Vegas, NV",
		Lat:            36.17,
		Lng:            -115.14,
		Language:       "en",
		FederationPeers: []string{
			"https://peer1.example.com",
			"https://peer2.example.com",
		},
	}
}

// testService returns a host.Service with a single empty group.
func testService() *host.Service {
	gid := types.GroupID{0xAA, 0xBB}
	state := group.NewState(gid)
	return host.NewService("test-host", state)
}

// ─── MCP Server construction ────────────────────────────────────────

func TestMCPServerConstruction(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	srv := mcp.NewServer(svc, cfg)

	if srv == nil {
		t.Fatal("NewServer returned nil")
	}

	// MCPServer should be non-nil (the underlying mcp-go server).
	mcpSrv := srv.MCPServer()
	if mcpSrv == nil {
		t.Fatal("MCPServer() returned nil")
	}

	// HTTPHandler should return a valid handler.
	_ = srv.HTTPHandler()
}

// ─── Discovery endpoints ────────────────────────────────────────────

func TestDiscoveryFederationEndpoint(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/federation", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "federated-meetup/v1") {
		t.Errorf("response missing protocol version: %s", body)
	}
	if !strings.Contains(body, "Test Host") {
		t.Errorf("response missing host name: %s", body)
	}
	if !strings.Contains(body, "Las Vegas") {
		t.Errorf("response missing geographic area: %s", body)
	}
	if !strings.Contains(body, "peer1.example.com") {
		t.Errorf("response missing federation peers: %s", body)
	}
	if !strings.Contains(body, "/mcp") {
		t.Errorf("response missing MCP endpoint: %s", body)
	}
	if !strings.Contains(body, "/openapi.json") {
		t.Errorf("response missing OpenAPI spec URL: %s", body)
	}

	// Verify it's valid JSON.
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if doc["protocol"] != "federated-meetup/v1" {
		t.Errorf("protocol field = %v, want federated-meetup/v1", doc["protocol"])
	}
}

func TestDiscoveryLLMsTxt(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/llms.txt", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Test Host") {
		t.Errorf("llms.txt missing host name: %s", body)
	}
	if !strings.Contains(body, "federated-meetup") {
		t.Errorf("llms.txt missing protocol reference: %s", body)
	}
	if !strings.Contains(body, "/mcp") {
		t.Errorf("llms.txt missing MCP endpoint: %s", body)
	}
	if !strings.Contains(body, "/feeds/") {
		t.Errorf("llms.txt missing feeds section: %s", body)
	}

	// Should be plain text, not JSON.
	contentType := rec.Header().Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		t.Errorf("llms.txt should be text/plain, got Content-Type: %s", contentType)
	}
}

func TestDiscoveryOpenAPI(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Verify it's valid JSON and has OpenAPI structure.
	var spec map[string]any
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}

	if spec["openapi"] != "3.1.0" {
		t.Errorf("openapi version = %v, want 3.1.0", spec["openapi"])
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("openapi.json missing paths")
	}

	// Check for key endpoints (paths are relative to /api/v1/ base).
	expectedPaths := []string{
		"/list-groups",
		"/list-events",
		"/get-group",
		"/resolve-name",
	}
	for _, p := range expectedPaths {
		if _, ok := paths[p]; !ok {
			t.Errorf("openapi.json missing path: %s", p)
		}
	}
}

func TestDiscoveryRobotsTxt(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/robots.txt", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "User-agent: *") {
		t.Errorf("robots.txt missing User-agent: * — got: %s", body)
	}
	if !strings.Contains(body, "GPTBot") {
		t.Errorf("robots.txt missing GPTBot allowlist: %s", body)
	}
	if !strings.Contains(body, "ClaudeBot") {
		t.Errorf("robots.txt missing ClaudeBot allowlist: %s", body)
	}
	if !strings.Contains(body, "PerplexityBot") {
		t.Errorf("robots.txt missing PerplexityBot allowlist: %s", body)
	}
	if !strings.Contains(body, "Sitemap:") {
		t.Errorf("robots.txt missing Sitemap directive: %s", body)
	}
}

// ─── RegisterEndpoints integration ──────────────────────────────────

func TestRegisterEndpoints(t *testing.T) {
	svc := testService()
	cfg := testConfig()

	mux := http.NewServeMux()
	mcpSrv := mcp.RegisterEndpoints(mux, svc, cfg)

	if mcpSrv == nil {
		t.Fatal("RegisterEndpoints returned nil server")
	}

	// Test each endpoint via the mux.
	endpoints := []struct {
		path       string
		expectBody string
	}{
		{"/.well-known/federation", "federated-meetup/v1"},
		{"/llms.txt", "Test Host"},
		{"/openapi.json", "openapi"},
		{"/robots.txt", "User-agent"},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", ep.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d", ep.path, rec.Code)
			}

			body := rec.Body.String()
			if !strings.Contains(body, ep.expectBody) {
				t.Fatalf("%s: response missing %q — got: %s", ep.path, ep.expectBody, body[:min(len(body), 200)])
			}
		})
	}
}

// ─── MCP HTTP handler ───────────────────────────────────────────────

func TestMCPHTTPHandler(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	srv := mcp.NewServer(svc, cfg)
	handler := srv.HTTPHandler()

	// Test that the MCP endpoint responds to an initialize request.
	// The MCP protocol uses JSON-RPC 2.0 over HTTP.
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(initReq))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	// The MCP server should respond (200 or 202 depending on transport).
	if rec.Code == 0 || rec.Code >= 400 {
		t.Fatalf("MCP initialize: expected < 400, got %d — body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "jsonrpc") {
		t.Errorf("MCP response missing jsonrpc field: %s", body[:min(len(body), 200)])
	}
}

// ─── Discovery with multiple groups ─────────────────────────────────

func TestDiscoveryWithMultipleGroups(t *testing.T) {
	gid1 := types.GroupID{0x01}
	gid2 := types.GroupID{0x02}
	state1 := group.NewState(gid1)
	state2 := group.NewState(gid2)
	svc := host.NewService("multi-host", state1, state2)
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	// Test federation endpoint shows group count.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/federation", nil)
	disc.ServeHTTP(rec, req)

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	// group_count should be 2 (we created two groups).
	groupCount, ok := doc["group_count"].(float64)
	if !ok {
		t.Fatalf("group_count is not a number: %v", doc["group_count"])
	}
	if int(groupCount) != 2 {
		t.Errorf("group_count = %d, want 2", int(groupCount))
	}
}

// ─── Discovery content types ────────────────────────────────────────

func TestDiscoveryContentTypes(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	tests := []struct {
		path     string
		expectCT string
	}{
		{"/.well-known/federation", "application/json"},
		{"/llms.txt", "text/plain"},
		{"/openapi.json", "application/json"},
		{"/robots.txt", "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tt.path, nil)
			disc.ServeHTTP(rec, req)

			ct := rec.Header().Get("Content-Type")
			if !strings.Contains(ct, tt.expectCT) {
				t.Errorf("%s: Content-Type = %q, want %q", tt.path, ct, tt.expectCT)
			}
		})
	}
}

// ─── Discovery 404 for unknown paths ────────────────────────────────

func TestDiscoveryUnknownPath(t *testing.T) {
	svc := testService()
	cfg := testConfig()
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/unknown-path", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: expected 404, got %d", rec.Code)
	}
}

// ─── HostConfig defaults ────────────────────────────────────────────

func TestHostConfigEmpty(t *testing.T) {
	svc := testService()
	// Empty config — should not panic.
	cfg := mcp.HostConfig{}
	disc := mcp.NewDiscoveryHandler(svc, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/federation", nil)
	disc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with empty config, got %d", rec.Code)
	}
}

// ─── Full HTTP server integration ───────────────────────────────────

func TestFullServerIntegration(t *testing.T) {
	svc := testService()
	cfg := testConfig()

	mux := http.NewServeMux()
	mcp.RegisterEndpoints(mux, svc, cfg)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test /.well-known/federation
	resp, err := http.Get(server.URL + "/.well-known/federation")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("federation endpoint: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "federated-meetup/v1") {
		t.Error("federation endpoint missing protocol version")
	}

	// Test /robots.txt
	resp, err = http.Get(server.URL + "/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("robots endpoint: %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "GPTBot") {
		t.Error("robots.txt missing GPTBot")
	}

	// Test /openapi.json
	resp, err = http.Get(server.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi endpoint: %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var spec map[string]any
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatalf("openapi.json invalid: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Errorf("openapi version = %v, want 3.1.0", spec["openapi"])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}