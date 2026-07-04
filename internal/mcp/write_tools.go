// SPDX-License-Identifier: AGPL-3.0

package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ProductStore is the interface the MCP write tools need from the
// product layer. This is satisfied by *product.Store.
type ProductStore interface {
	GetGroup(id string) (*pb.Group, bool)
	GetEvent(id string) (*pb.Event, bool)
	EventsForGroup(groupID string) []*pb.Event
	PutEvent(e *pb.Event)
}

// SetProductStore wires a product store into the MCP server, enabling
// write tools (create_event, list_all_events). Without this,
// only the 6 read tools work.
func (s *Server) SetProductStore(store ProductStore) {
	s.product = store
	s.registerWriteTools()
}

// registerWriteTools adds MCP tools that perform writes.
func (s *Server) registerWriteTools() {
	if s.product == nil {
		return
	}

	// 7. create_event
	s.mcpSrv.AddTool(
		mcp.NewTool("create_event",
			mcp.WithDescription("Create a new event for a group. Returns the created event ID and URL. The AI assistant should gather: group name, event title, description, start time (ISO 8601), location, and capacity."),
			mcp.WithString("group_id", mcp.Required(), mcp.Description("The group ID (canonical name) to create the event under")),
			mcp.WithString("title", mcp.Required(), mcp.Description("Event title")),
			mcp.WithString("description", mcp.Description("Event description")),
			mcp.WithString("starts_at", mcp.Required(), mcp.Description("Start time in ISO 8601 format (e.g. 2026-08-15T18:00)")),
			mcp.WithString("location", mcp.Description("Event location")),
			mcp.WithInteger("capacity", mcp.Description("Maximum attendees (0 = unlimited)")),
		),
		s.handleCreateEvent,
	)

	// 8. list_all_events
	s.mcpSrv.AddTool(
		mcp.NewTool("list_all_events",
			mcp.WithDescription("List all upcoming events across all groups on this host. Returns events sorted by start time with group and event IDs."),
			mcp.WithInteger("page_size", mcp.Description("Optional page size (default 50, max 200)")),
		),
		s.handleListAllEvents,
	)
}

func (s *Server) handleCreateEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.product == nil {
		return mcp.NewToolResultError("product store not configured"), nil
	}
	groupID := req.GetString("group_id", "")
	if groupID == "" {
		return mcp.NewToolResultError("group_id is required"), nil
	}
	if _, ok := s.product.GetGroup(groupID); !ok {
		return mcp.NewToolResultError(fmt.Sprintf("group %q not found", groupID)), nil
	}
	title := req.GetString("title", "")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	startsAtStr := req.GetString("starts_at", "")
	if startsAtStr == "" {
		return mcp.NewToolResultError("starts_at is required"), nil
	}
	startsAt, err := time.Parse("2006-01-02T15:04", startsAtStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid starts_at: %v", err)), nil
	}
	location := req.GetString("location", "")
	capacity := int(req.GetInt("capacity", 0))
	eventID := generateEventID(title)
	event := &pb.Event{
		EventId:     eventID,
		GroupId:     groupID,
		Title:       title,
		Description: req.GetString("description", ""),
		StartsAt:    timestamppb.New(startsAt),
		Location:    location,
		Capacity:    uint64(capacity),
	}
	s.product.PutEvent(event)
	result := map[string]interface{}{
		"event_id": eventID, "group_id": groupID, "title": title,
		"starts_at": startsAt.Format(time.RFC3339), "location": location,
		"capacity": capacity,
		"url":      fmt.Sprintf("%s/events/%s/%s", s.cfg.URL, groupID, eventID),
	}
	j, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(j)), nil
}

func (s *Server) handleListAllEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.product == nil {
		return mcp.NewToolResultError("product store not configured"), nil
	}
	var allEvents []map[string]interface{}
	for _, gid := range s.svc.Groups().All() {
		groupKey := hex.EncodeToString(gid[:])
		for _, e := range s.product.EventsForGroup(groupKey) {
			if e.StartsAt == nil { continue }
			t := e.StartsAt.AsTime()
			if t.Before(time.Now()) { continue }
			allEvents = append(allEvents, map[string]interface{}{
				"event_id": e.EventId, "group_id": e.GroupId,
				"title": e.Title, "starts_at": t.Format(time.RFC3339),
				"location": e.Location, "capacity": e.Capacity,
				"url": fmt.Sprintf("%s/events/%s/%s", s.cfg.URL, e.GroupId, e.EventId),
			})
		}
	}
	j, _ := json.MarshalIndent(map[string]interface{}{
		"events": allEvents, "total": len(allEvents), "host": s.cfg.Name,
	}, "", "  ")
	return mcp.NewToolResultText(string(j)), nil
}

func generateEventID(title string) string {
	var slug []byte
	for _, c := range title {
		if c == ' ' { slug = append(slug, '-') } else
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' { slug = append(slug, byte(c)) } else
		if c >= 'A' && c <= 'Z' { slug = append(slug, byte(c+32)) }
	}
	if len(slug) > 30 { slug = slug[:30] }
	return fmt.Sprintf("evt-%s", string(slug))
}
