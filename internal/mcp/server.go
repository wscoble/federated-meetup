// SPDX-License-Identifier: MIT
//
// Package mcp implements the MCP (Model Context Protocol) server for the
// federated-meetup host. It wraps the host's existing read RPCs (implemented
// in internal/host.Service) as MCP tools that AI assistants can call.
//
// The MCP server exposes six tools:
//   - list_groups   — list all groups on this host
//   - list_events   — list upcoming events for a group
//   - get_event     — get details for a single event
//   - resolve_name  — resolve a canonical name to a group
//   - get_group     — get group details
//   - find_events   — search for events by location and/or interest
//
// Transport: HTTP (POST /mcp) using MCP JSON-RPC framing via the
// streamable-http transport from github.com/mark3labs/mcp-go.
package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"connectrpc.com/connect"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/proto/federated_meetup/v1/federatedmeetupv1connect"

	"github.com/sscoble/federated-meetup/internal/host"
	"github.com/sscoble/federated-meetup/internal/types"
)

// HostConfig holds the metadata used to build discovery endpoints and
// MCP tool responses that reference the host's identity.
type HostConfig struct {
	// Name is the human-readable host name (e.g. "Vegas Programmers Host").
	Name string
	// URL is the base URL of the host (e.g. "https://vegasprogrammers.org").
	URL string
	// Description is a short description of the host.
	Description string
	// GeographicArea is the human-readable area (e.g. "Las Vegas, NV").
	GeographicArea string
	// Lat is the latitude of the host's service area.
	Lat float64
	// Lng is the longitude of the host's service area.
	Lng float64
	// Language is the primary language code (e.g. "en").
	Language string
	// FederationPeers is the list of peer host URLs.
	FederationPeers []string
}

// Server is the MCP server for a federated-meetup host. It wraps the
// host's ConnectRPC Service as MCP tools.
type Server struct {
	cfg    HostConfig
	svc    *host.Service
	mcpSrv *server.MCPServer
}

// NewServer constructs an MCP server bound to the given host Service and
// host configuration. The returned Server's HTTPHandler() method returns
// an http.Handler suitable for mounting at /mcp.
func NewServer(svc *host.Service, cfg HostConfig) *Server {
	s := &Server{
		cfg: cfg,
		svc: svc,
	}

	mcpSrv := server.NewMCPServer(
		cfg.Name,
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	s.mcpSrv = mcpSrv
	s.registerTools()

	return s
}

// HTTPHandler returns the http.Handler for the MCP streamable-http transport.
// Mount it at POST /mcp.
func (s *Server) HTTPHandler() server.StreamableHTTPServer {
	return *server.NewStreamableHTTPServer(
		s.mcpSrv,
		server.WithEndpointPath("/mcp"),
	)
}

// MCPServer returns the underlying mcp-go MCPServer (for testing).
func (s *Server) MCPServer() *server.MCPServer { return s.mcpSrv }

// ----- Tool registration ---------------------------------------------------

func (s *Server) registerTools() {
	// 1. list_groups
	s.mcpSrv.AddTool(
		mcp.NewTool("list_groups",
			mcp.WithDescription("List all groups hosted by this host. Returns an array of group summaries with group_key, canonical_name, and display_name."),
			mcp.WithString("name_contains",
				mcp.Description("Optional substring filter on canonical name"),
			),
			mcp.WithInteger("page_size",
				mcp.Description("Optional page size (default 50, max 200)"),
			),
		),
		s.handleListGroups,
	)

	// 2. list_events
	s.mcpSrv.AddTool(
		mcp.NewTool("list_events",
			mcp.WithDescription("List upcoming events for a group. Returns an array of events with event_id, title, starts_at, location, capacity, and rsvp_count."),
			mcp.WithString("group_key",
				mcp.Required(),
				mcp.Description("Hex-encoded 32-byte group public key"),
			),
			mcp.WithInteger("page_size",
				mcp.Description("Optional page size (default 50, max 200)"),
			),
		),
		s.handleListEvents,
	)

	// 3. get_event
	s.mcpSrv.AddTool(
		mcp.NewTool("get_event",
			mcp.WithDescription("Get details for a single event. Returns event_id, title, starts_at, ends_at, location, capacity, rsvp_count, cancelled, and description."),
			mcp.WithString("group_key",
				mcp.Required(),
				mcp.Description("Hex-encoded 32-byte group public key"),
			),
			mcp.WithString("event_id",
				mcp.Required(),
				mcp.Description("The event ID to look up"),
			),
		),
		s.handleGetEvent,
	)

	// 4. resolve_name
	s.mcpSrv.AddTool(
		mcp.NewTool("resolve_name",
			mcp.WithDescription("Resolve a canonical name to a group key and the hosts serving it. Returns group_key (hex) and an array of host URLs."),
			mcp.WithString("canonical_name",
				mcp.Required(),
				mcp.Description("Canonical name to resolve (e.g. \"vegas-programmers\")"),
			),
		),
		s.handleResolveName,
	)

	// 5. get_group
	s.mcpSrv.AddTool(
		mcp.NewTool("get_group",
			mcp.WithDescription("Get group details. Provide either group_key (hex) or canonical_name. Returns group_key, canonical_name, display_name, steward_count, threshold, event_count, and member_count."),
			mcp.WithString("group_key",
				mcp.Description("Hex-encoded 32-byte group public key (optional if canonical_name is provided)"),
			),
			mcp.WithString("canonical_name",
				mcp.Description("Canonical name of the group (optional if group_key is provided)"),
			),
		),
		s.handleGetGroup,
	)

	// 6. find_events
	s.mcpSrv.AddTool(
		mcp.NewTool("find_events",
			mcp.WithDescription("Search for events by location and/or interest across all groups on this host. Returns an array of matching events. This is a host-level search, not federation-wide."),
			mcp.WithString("location",
				mcp.Description("Optional location filter (e.g. \"Las Vegas\")"),
			),
			mcp.WithString("interest",
				mcp.Description("Optional interest keyword (e.g. \"programming\")"),
			),
			mcp.WithString("when",
				mcp.Description("Optional time filter (e.g. \"this weekend\", \"next week\")"),
			),
		),
		s.handleFindEvents,
	)
}

// ----- Tool handlers -------------------------------------------------------

// handleListGroups wraps the host's ListGroups RPC.
func (s *Server) handleListGroups(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	nameContains := req.GetString("name_contains", "")
	pageSize := req.GetInt("page_size", 0)

	rpcReq := connect.NewRequest(&pb.ListGroupsRequest{
		NameContains: nameContains,
		PageSize:     uint32(pageSize),
	})
	resp, err := s.svc.ListGroups(ctx, rpcReq)
	if err != nil {
		return toolError(fmt.Sprintf("list_groups failed: %v", err)), nil
	}

	groups := make([]map[string]any, 0, len(resp.Msg.GetGroups()))
	for _, g := range resp.Msg.GetGroups() {
		key := g.GetGroupKey()
		groups = append(groups, map[string]any{
			"group_key":      hex.EncodeToString(key.GetRaw()),
			"canonical_name": g.GetCanonicalName(),
			"display_name":   g.GetDisplayName(),
		})
	}

	result := map[string]any{
		"groups":      groups,
		"next_cursor": resp.Msg.GetNextCursor(),
	}
	return toolJSONResult(result)
}

// handleListEvents wraps the host's ListEvents RPC.
func (s *Server) handleListEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	groupKeyHex := req.GetString("group_key", "")
	if groupKeyHex == "" {
		return toolError("group_key is required"), nil
	}
	groupKey, err := parseHexKey(groupKeyHex)
	if err != nil {
		return toolError(fmt.Sprintf("invalid group_key: %v", err)), nil
	}
	pageSize := req.GetInt("page_size", 0)

	rpcReq := connect.NewRequest(&pb.ListEventsRequest{
		GroupKey: &pb.PublicKey{Raw: groupKey[:]},
		PageSize: uint32(pageSize),
	})
	resp, err := s.svc.ListEvents(ctx, rpcReq)
	if err != nil {
		return toolError(fmt.Sprintf("list_events failed: %v", err)), nil
	}

	events := make([]map[string]any, 0, len(resp.Msg.GetEvents()))
	for _, e := range resp.Msg.GetEvents() {
		events = append(events, eventToMap(e))
	}

	result := map[string]any{
		"events":      events,
		"next_cursor": resp.Msg.GetNextCursor(),
	}
	return toolJSONResult(result)
}

// handleGetEvent wraps the host's GetEvent RPC.
func (s *Server) handleGetEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	groupKeyHex := req.GetString("group_key", "")
	if groupKeyHex == "" {
		return toolError("group_key is required"), nil
	}
	groupKey, err := parseHexKey(groupKeyHex)
	if err != nil {
		return toolError(fmt.Sprintf("invalid group_key: %v", err)), nil
	}
	eventID := req.GetString("event_id", "")
	if eventID == "" {
		return toolError("event_id is required"), nil
	}

	rpcReq := connect.NewRequest(&pb.GetEventRequest{
		GroupKey: &pb.PublicKey{Raw: groupKey[:]},
		EventId:  eventID,
	})
	resp, err := s.svc.GetEvent(ctx, rpcReq)
	if err != nil {
		return toolError(fmt.Sprintf("get_event failed: %v", err)), nil
	}

	result := map[string]any{
		"event":     eventToMap(resp.Msg.GetEvent()),
		"rsvp_count": len(resp.Msg.GetRsvps()),
		"cancelled":  resp.Msg.GetCancelled(),
	}
	return toolJSONResult(result)
}

// handleResolveName wraps the host's ResolveName RPC.
func (s *Server) handleResolveName(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	canonicalName := req.GetString("canonical_name", "")
	if canonicalName == "" {
		return toolError("canonical_name is required"), nil
	}

	rpcReq := connect.NewRequest(&pb.ResolveNameRequest{
		CanonicalName: canonicalName,
	})
	resp, err := s.svc.ResolveName(ctx, rpcReq)
	if err != nil {
		return toolError(fmt.Sprintf("resolve_name failed: %v", err)), nil
	}

	result := map[string]any{
		"group_key": hex.EncodeToString(resp.Msg.GetGroupKey().GetRaw()),
		"hosts":     resp.Msg.GetHosts(),
	}
	return toolJSONResult(result)
}

// handleGetGroup wraps the host's GetGroup RPC.
func (s *Server) handleGetGroup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	groupKeyHex := req.GetString("group_key", "")
	canonicalName := req.GetString("canonical_name", "")

	if groupKeyHex == "" && canonicalName == "" {
		return toolError("either group_key or canonical_name is required"), nil
	}

	rpcReq := &pb.GetGroupRequest{}
	if groupKeyHex != "" {
		groupKey, err := parseHexKey(groupKeyHex)
		if err != nil {
			return toolError(fmt.Sprintf("invalid group_key: %v", err)), nil
		}
		rpcReq.Identifier = &pb.GetGroupRequest_GroupKey{
			GroupKey: &pb.PublicKey{Raw: groupKey[:]},
		}
	} else {
		rpcReq.Identifier = &pb.GetGroupRequest_CanonicalName{
			CanonicalName: canonicalName,
		}
	}

	rpcCall := connect.NewRequest(rpcReq)
	resp, err := s.svc.GetGroup(ctx, rpcCall)
	if err != nil {
		return toolError(fmt.Sprintf("get_group failed: %v", err)), nil
	}

	snap := resp.Msg.GetSnapshot()
	stewardCount := len(resp.Msg.GetStewards())
	threshold := resp.Msg.GetThreshold()

	// Extract group metadata from the state KV if available.
	canonicalNameOut := ""
	displayName := ""
	memberCount := 0
	eventCount := 0
	for _, e := range snap.GetEntries() {
		switch e.GetKey() {
		case "canonical_name":
			canonicalNameOut = string(e.GetValue())
		case "display_name":
			displayName = string(e.GetValue())
		case "member_count":
			if len(e.GetValue()) >= 1 {
				memberCount = int(e.GetValue()[0])
			}
		case "event_count":
			if len(e.GetValue()) >= 1 {
				eventCount = int(e.GetValue()[0])
			}
		}
	}

	// Try to get the group key from the snapshot root or from the request.
	var groupKeyOut string
	if groupKeyHex != "" {
		groupKeyOut = groupKeyHex
	} else {
		// Use the first group from the registry.
		gids := s.svc.Groups().All()
		if len(gids) > 0 {
			groupKeyOut = hex.EncodeToString(gids[0][:])
		}
	}

	result := map[string]any{
		"group_key":      groupKeyOut,
		"canonical_name": canonicalNameOut,
		"display_name":   displayName,
		"steward_count":  stewardCount,
		"threshold":      threshold,
		"event_count":    eventCount,
		"member_count":   memberCount,
	}
	return toolJSONResult(result)
}

// handleFindEvents searches across all groups on this host for events
// matching the given location and/or interest. This is a host-level
// search — for federation-wide search, the AI agent queries multiple hosts.
func (s *Server) handleFindEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	location := strings.ToLower(req.GetString("location", ""))
	interest := strings.ToLower(req.GetString("interest", ""))
	when := req.GetString("when", "")

	gids := s.svc.Groups().All()
	allEvents := make([]map[string]any, 0)

	for _, gid := range gids {
		rpcReq := connect.NewRequest(&pb.ListEventsRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
			PageSize: 200,
		})
		resp, err := s.svc.ListEvents(ctx, rpcReq)
		if err != nil {
			continue // skip groups that fail
		}
		for _, e := range resp.Msg.GetEvents() {
			ev := eventToMap(e)
			ev["group"] = hex.EncodeToString(gid[:])

			// Filter by location (substring match on event location).
			if location != "" {
				evLoc := strings.ToLower(e.GetLocation())
				if !strings.Contains(evLoc, location) {
					continue
				}
			}

			// Filter by interest (substring match on title).
			if interest != "" {
				evTitle := strings.ToLower(e.GetTitle())
				if !strings.Contains(evTitle, interest) {
					continue
				}
			}

			// Filter by "when" — simple time-window matching.
			if when != "" {
				if !matchWhen(e.GetStartsAt(), when) {
					continue
				}
			}

			// Add URL if host URL is configured.
			if s.cfg.URL != "" {
				ev["url"] = fmt.Sprintf("%s/events/%s", s.cfg.URL, e.GetEventId())
			}

			allEvents = append(allEvents, ev)
		}
	}

	result := map[string]any{
		"events": allEvents,
		"count":  len(allEvents),
	}
	return toolJSONResult(result)
}

// ----- Helpers -------------------------------------------------------------

// parseHexKey parses a hex-encoded 32-byte public key.
func parseHexKey(hexStr string) (types.PublicKey, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return types.PublicKey{}, fmt.Errorf("hex decode: %w", err)
	}
	if len(b) != 32 {
		return types.PublicKey{}, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	var key types.PublicKey
	copy(key[:], b)
	return key, nil
}

// eventToMap converts a CreateEventPayload to a JSON-serializable map.
func eventToMap(e *pb.CreateEventPayload) map[string]any {
	if e == nil {
		return nil
	}
	m := map[string]any{
		"event_id": e.GetEventId(),
		"title":    e.GetTitle(),
		"location": e.GetLocation(),
		"capacity": e.GetCapacity(),
		"paid":     e.GetPaid(),
	}
	if e.GetStartsAt() != nil {
		m["starts_at"] = tsToRFC3339(e.GetStartsAt())
	}
	if e.GetEndsAt() != nil {
		m["ends_at"] = tsToRFC3339(e.GetEndsAt())
	}
	return m
}

// tsToRFC3339 converts a protobuf timestamp to an RFC 3339 string.
func tsToRFC3339(ts interface{ GetSeconds() int64; GetNanos() int32 }) string {
	sec := ts.GetSeconds()
	nanos := ts.GetNanos()
	t := time.Unix(sec, int64(nanos)).UTC()
	return t.Format(time.RFC3339)
}

// matchWhen does a simple time-window match for "when" strings.
// Supported: "this weekend", "next week", "today", "tomorrow".
// For unsupported strings, returns true (no filtering).
func matchWhen(ts interface{ GetSeconds() int64; GetNanos() int32 }, when string) bool {
	when = strings.ToLower(strings.TrimSpace(when))
	if when == "" {
		return true
	}
	if ts == nil {
		return true
	}
	t := time.Unix(ts.GetSeconds(), int64(ts.GetNanos())).UTC()
	now := time.Now().UTC()

	switch {
	case when == "today":
		return t.Format("2006-01-02") == now.Format("2006-01-02")
	case when == "tomorrow":
		tom := now.AddDate(0, 0, 1)
		return t.Format("2006-01-02") == tom.Format("2006-01-02")
	case when == "this weekend":
		// Find the coming Saturday.
		daysUntilSat := (6 - int(now.Weekday()) + 7) % 7
		if daysUntilSat == 0 && now.Weekday() == time.Sunday {
			daysUntilSat = 6 // Sunday: next Saturday is 6 days away
		}
		sat := now.AddDate(0, 0, daysUntilSat)
		sun := sat.AddDate(0, 0, 1)
		return t.Format("2006-01-02") == sat.Format("2006-01-02") ||
			t.Format("2006-01-02") == sun.Format("2006-01-02")
	case when == "next week":
		nextWeekStart := now.AddDate(0, 0, 7)
		nextWeekEnd := nextWeekStart.AddDate(0, 0, 7)
		return (t.After(nextWeekStart) || t.Equal(nextWeekStart)) && t.Before(nextWeekEnd)
	default:
		return true
	}
}

// toolError creates a CallToolResult with isError=true and a text message.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}

// toolJSONResult creates a CallToolResult with a JSON content payload.
func toolJSONResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: string(b)},
		},
		StructuredContent: data,
	}, nil
}

// Compile-time assertion: Server implements nothing specific at the
// Go type level — it's composed of mcp-go types. The assertion below
// verifies that the host Service satisfies the generated handler interface.
var _ federatedmeetupv1connect.HostServiceHandler = (*host.Service)(nil)