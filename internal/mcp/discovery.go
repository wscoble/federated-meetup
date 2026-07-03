// SPDX-License-Identifier: AGPL-3.0
//
// Discovery endpoints for the federated-meetup host: /.well-known/federation,
// /llms.txt, /openapi.json, /robots.txt. These are served alongside the
// ConnectRPC API on the host's HTTP server.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"

	"github.com/sscoble/federated-meetup/internal/host"
)

// FederationDocument is the JSON response for /.well-known/federation.
// Per docs/08-DISCOVERY.md §3.1.
type FederationDocument struct {
	Protocol           string            `json:"protocol"`
	Host               HostInfo          `json:"host"`
	GroupsEndpoint     string            `json:"groups_endpoint"`
	EventsEndpoint     string            `json:"events_endpoint"`
	ResolveNameEndpoint string           `json:"resolve_name_endpoint"`
	ConnectRPCEndpoint string            `json:"connectrpc_endpoint"`
	MCPEndpoint        string            `json:"mcp_endpoint"`
	OpenAPISpec        string            `json:"openapi_spec"`
	Feeds              map[string]string `json:"feeds"`
	GroupCount         int               `json:"group_count"`
	EventCountThisWeek int               `json:"event_count_this_week"`
	FederationPeers    []string          `json:"federation_peers"`
}

// HostInfo describes the host in the federation document.
type HostInfo struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	Description    string  `json:"description"`
	GeographicArea string  `json:"geographic_area"`
	Coordinates    Coord   `json:"coordinates"`
	Language       string  `json:"language"`
}

// Coord is a lat/lng pair.
type Coord struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// DiscoveryHandler is an http.Handler that serves the four discovery
// endpoints. Mount it at the root of the host's HTTP mux; it dispatches
// by path.
type DiscoveryHandler struct {
	svc *host.Service
	cfg HostConfig
}

// NewDiscoveryHandler creates a DiscoveryHandler for the given host
// service and configuration.
func NewDiscoveryHandler(svc *host.Service, cfg HostConfig) *DiscoveryHandler {
	return &DiscoveryHandler{svc: svc, cfg: cfg}
}

// ServeHTTP dispatches discovery endpoint requests by path.
func (h *DiscoveryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/federation":
		h.handleFederation(w, r)
	case "/llms.txt":
		h.handleLLMsTxt(w, r)
	case "/openapi.json":
		h.handleOpenAPI(w, r)
	case "/robots.txt":
		h.handleRobotsTxt(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleFederation serves the /.well-known/federation endpoint.
func (h *DiscoveryHandler) handleFederation(w http.ResponseWriter, r *http.Request) {
	groupCount := h.svc.Groups().Len()
	eventCountThisWeek := h.countEventsThisWeek(r.Context())

	baseURL := h.cfg.URL
	if baseURL == "" {
		baseURL = "http://" + r.Host
	}

	doc := FederationDocument{
		Protocol: "federated-meetup/v1",
		Host: HostInfo{
			Name:           h.cfg.Name,
			URL:            baseURL,
			Description:    h.cfg.Description,
			GeographicArea: h.cfg.GeographicArea,
			Coordinates:    Coord{Lat: h.cfg.Lat, Lng: h.cfg.Lng},
			Language:       h.cfg.Language,
		},
		GroupsEndpoint:      "/api/v1/list-groups",
		EventsEndpoint:      "/api/v1/list-events",
		ResolveNameEndpoint: "/api/v1/resolve-name",
		ConnectRPCEndpoint:  "/api/v1/",
		MCPEndpoint:         "/mcp",
		OpenAPISpec:         "/openapi.json",
		Feeds: map[string]string{
			"rss":   "/feeds/all.rss",
			"atom":  "/feeds/all.atom",
			"json":  "/feeds/all.json",
		},
		GroupCount:         groupCount,
		EventCountThisWeek: eventCountThisWeek,
		FederationPeers:    h.cfg.FederationPeers,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}

// handleLLMsTxt serves the /llms.txt endpoint.
// Format per docs/08-DISCOVERY.md §3.2.
func (h *DiscoveryHandler) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	baseURL := h.cfg.URL
	if baseURL == "" {
		baseURL = "http://" + r.Host
	}

	var b strings.Builder

	// Title and description.
	fmt.Fprintf(&b, "# %s\n\n", h.cfg.Name)
	if h.cfg.Description != "" {
		fmt.Fprintf(&b, "> %s\n\n", h.cfg.Description)
	}

	// Groups section.
	b.WriteString("## Groups\n\n")
	gids := h.svc.Groups().All()
	for _, gid := range gids {
		canonicalName, displayName, description := h.getGroupMetadata(r.Context(), gid)
		if canonicalName == "" {
			canonicalName = fmt.Sprintf("group-%x", gid[:8])
		}
		if displayName == "" {
			displayName = canonicalName
		}
		fmt.Fprintf(&b, "### %s\n", canonicalName)
		fmt.Fprintf(&b, "- Display name: %s\n", displayName)
		if description != "" {
			fmt.Fprintf(&b, "- Description: %s\n", description)
		}
		fmt.Fprintf(&b, "- URL: %s/groups/%s\n\n", baseURL, canonicalName)
	}

	// API section.
	b.WriteString("## API\n\n")
	b.WriteString("This host speaks the federated-meetup v1 protocol.\n")
	b.WriteString("- List groups: GET /api/v1/list-groups\n")
	b.WriteString("- List events: GET /api/v1/list-events?group_key=<pubkey>\n")
	b.WriteString("- Resolve name: GET /api/v1/resolve-name?canonical_name=<name>\n")
	b.WriteString("- OpenAPI spec: GET /openapi.json\n")
	b.WriteString("- MCP server: POST /mcp\n\n")

	// Feeds section.
	b.WriteString("## Feeds\n\n")
	b.WriteString("- RSS: /feeds/all.rss\n")
	b.WriteString("- Atom: /feeds/all.atom\n")
	b.WriteString("- JSON Feed: /feeds/all.json\n")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// handleOpenAPI serves the /openapi.json endpoint.
// This is a hand-written OpenAPI 3.1 spec matching the protobuf definitions.
func (h *DiscoveryHandler) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	baseURL := h.cfg.URL
	if baseURL == "" {
		baseURL = "http://" + r.Host
	}

	spec := buildOpenAPISpec(baseURL)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(spec)
}

// handleRobotsTxt serves the /robots.txt endpoint.
// Per docs/08-DISCOVERY.md §2.3.4.
func (h *DiscoveryHandler) handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	baseURL := h.cfg.URL
	if baseURL == "" {
		baseURL = "http://" + r.Host
	}

	robots := fmt.Sprintf(`User-agent: *
Allow: /

# AI crawlers explicitly allowed
User-agent: GPTBot
Allow: /

User-agent: ClaudeBot
Allow: /

User-agent: PerplexityBot
Allow: /

User-agent: Google-Extended
Allow: /

User-agent: AppleBot
Allow: /

Sitemap: %s/sitemap.xml
`, baseURL)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(robots))
}

// ----- Helpers -------------------------------------------------------------

// countEventsThisWeek counts events across all groups that start within
// the next 7 days.
func (h *DiscoveryHandler) countEventsThisWeek(ctx context.Context) int {
	now := time.Now().UTC()
	weekEnd := now.AddDate(0, 0, 7)
	count := 0
	for _, gid := range h.svc.Groups().All() {
		rpcReq := connect.NewRequest(&pb.ListEventsRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
			PageSize: 200,
		})
		resp, err := h.svc.ListEvents(ctx, rpcReq)
		if err != nil {
			continue
		}
		for _, e := range resp.Msg.GetEvents() {
			if e.GetStartsAt() == nil {
				continue
			}
			t := time.Unix(e.GetStartsAt().GetSeconds(), int64(e.GetStartsAt().GetNanos())).UTC()
			if (t.After(now) || t.Equal(now)) && t.Before(weekEnd) {
				count++
			}
		}
	}
	return count
}

// getGroupMetadata extracts canonical_name, display_name, and description
// from a group's state KV.
func (h *DiscoveryHandler) getGroupMetadata(ctx context.Context, gid [32]byte) (canonicalName, displayName, description string) {
	rpcReq := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
		},
	})
	resp, err := h.svc.GetGroup(ctx, rpcReq)
	if err != nil {
		return
	}
	for _, e := range resp.Msg.GetSnapshot().GetEntries() {
		switch e.GetKey() {
		case "canonical_name":
			canonicalName = string(e.GetValue())
		case "display_name":
			displayName = string(e.GetValue())
		case "description":
			description = string(e.GetValue())
		}
	}
	return
}

// buildOpenAPISpec constructs a hand-written OpenAPI 3.1 spec matching
// the protobuf definitions in proto/federated_meetup/v1/rpc.proto.
func buildOpenAPISpec(baseURL string) map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Federated Meetup v1 API",
			"description": "Open client API for the federated-meetup protocol. Read endpoints are unauthenticated; write endpoints require signed transitions.",
			"version":     "1.0.0",
		},
		"servers": []map[string]any{
			{"url": baseURL + "/api/v1"},
		},
		"paths": map[string]any{
			"/list-groups": map[string]any{
				"get": map[string]any{
					"summary":     "List all groups on this host",
					"operationId": "listGroups",
					"parameters": []map[string]any{
						{"name": "name_contains", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "cursor", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "page_size", "in": "query", "schema": map[string]any{"type": "integer"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of groups",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ListGroupsResponse"},
								},
							},
						},
					},
				},
			},
			"/list-events": map[string]any{
				"get": map[string]any{
					"summary":     "List events for a group",
					"operationId": "listEvents",
					"parameters": []map[string]any{
						{"name": "group_key", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "hex"}},
						{"name": "cursor", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "page_size", "in": "query", "schema": map[string]any{"type": "integer"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of events",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ListEventsResponse"},
								},
							},
						},
					},
				},
			},
			"/get-group": map[string]any{
				"get": map[string]any{
					"summary":     "Get group details by key or name",
					"operationId": "getGroup",
					"parameters": []map[string]any{
						{"name": "group_key", "in": "query", "schema": map[string]any{"type": "string", "format": "hex"}},
						{"name": "canonical_name", "in": "query", "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Group details",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/GetGroupResponse"},
								},
							},
						},
					},
				},
			},
			"/get-event": map[string]any{
				"get": map[string]any{
					"summary":     "Get a single event",
					"operationId": "getEvent",
					"parameters": []map[string]any{
						{"name": "group_key", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "hex"}},
						{"name": "event_id", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Event details",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/GetEventResponse"},
								},
							},
						},
					},
				},
			},
			"/resolve-name": map[string]any{
				"get": map[string]any{
					"summary":     "Resolve a canonical name to a group key and hosting URLs",
					"operationId": "resolveName",
					"parameters": []map[string]any{
						{"name": "canonical_name", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Resolution result",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ResolveNameResponse"},
								},
							},
						},
					},
				},
			},
			"/submit-transition": map[string]any{
				"post": map[string]any{
					"summary":     "Submit a signed steward transition",
					"operationId": "submitTransition",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/SubmitTransitionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Transition applied",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/SubmitTransitionResponse"},
								},
							},
						},
					},
				},
			},
			"/submit-user-action": map[string]any{
				"post": map[string]any{
					"summary":     "Submit a user-signed action (RSVP, attest)",
					"operationId": "submitUserAction",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/SubmitUserActionRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Action applied",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/SubmitUserActionResponse"},
								},
							},
						},
					},
				},
			},
			"/get-log": map[string]any{
				"get": map[string]any{
					"summary":     "Get transition log (paginated)",
					"operationId": "getLog",
					"parameters": []map[string]any{
						{"name": "group_key", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "hex"}},
						{"name": "since_cursor", "in": "query", "schema": map[string]any{"type": "integer"}},
						{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Transition log",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/GetLogResponse"},
								},
							},
						},
					},
				},
			},
			"/get-snapshot": map[string]any{
				"get": map[string]any{
					"summary":     "Get state snapshot",
					"operationId": "getSnapshot",
					"parameters": []map[string]any{
						{"name": "group_key", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "hex"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "State snapshot",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/GetSnapshotResponse"},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"PublicKey": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"raw": map[string]any{"type": "string", "format": "byte"},
					},
				},
				"ListGroupsResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"groups": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"group_key":     map[string]any{"$ref": "#/components/schemas/PublicKey"},
									"canonical_name": map[string]any{"type": "string"},
									"display_name":   map[string]any{"type": "string"},
								},
							},
						},
						"next_cursor": map[string]any{"type": "string"},
					},
				},
				"ListEventsResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"events": map[string]any{
							"type": "array",
							"items": map[string]any{"$ref": "#/components/schemas/CreateEventPayload"},
						},
						"next_cursor": map[string]any{"type": "string"},
					},
				},
				"CreateEventPayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"event_id":  map[string]any{"type": "string"},
						"title":     map[string]any{"type": "string"},
						"starts_at": map[string]any{"type": "string", "format": "date-time"},
						"ends_at":   map[string]any{"type": "string", "format": "date-time"},
						"location":  map[string]any{"type": "string"},
						"capacity":  map[string]any{"type": "integer"},
						"paid":      map[string]any{"type": "boolean"},
					},
				},
				"GetGroupResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot":  map[string]any{"type": "object"},
						"stewards":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/PublicKey"}},
						"threshold": map[string]any{"type": "integer"},
					},
				},
				"GetEventResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"event":    map[string]any{"$ref": "#/components/schemas/CreateEventPayload"},
						"rsvps":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/PublicKey"}},
						"cancelled": map[string]any{"type": "boolean"},
					},
				},
				"ResolveNameResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"group_key": map[string]any{"$ref": "#/components/schemas/PublicKey"},
						"hosts":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
				"SubmitTransitionRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"group_key":   map[string]any{"$ref": "#/components/schemas/PublicKey"},
						"transition":  map[string]any{"type": "object"},
					},
				},
				"SubmitTransitionResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"new_snapshot":      map[string]any{"type": "object"},
						"transition_index":  map[string]any{"type": "integer"},
					},
				},
				"SubmitUserActionRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"group_key":           map[string]any{"$ref": "#/components/schemas/PublicKey"},
						"type":                map[string]any{"type": "string"},
						"user_envelope":       map[string]any{"type": "object"},
						"transition_payload":  map[string]any{"type": "string", "format": "byte"},
					},
				},
				"SubmitUserActionResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"new_snapshot": map[string]any{"type": "object"},
					},
				},
				"GetLogResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"transitions":  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
						"next_cursor":  map[string]any{"type": "integer"},
						"total":        map[string]any{"type": "integer"},
					},
				},
				"GetSnapshotResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot":         map[string]any{"type": "object"},
						"transition_index": map[string]any{"type": "integer"},
					},
				},
			},
		},
	}
}