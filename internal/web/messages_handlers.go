// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// ---- Message data types for templates ----

// messageViewData holds a message and its replies for template rendering.
type messageViewData struct {
	Message   GroupMessage
	Replies   []GroupMessage
	Reactions []ReactionSummary
	GroupKey  string
	CSRFToken string
	UserEmail string
}

// messageListData is the data for the message list fragment.
type messageListData struct {
	Messages  []messageViewData
	GroupKey  string
	HasMore   bool
	Cursor    int64
	CSRFToken string
	UserEmail string
	SearchQ   string
}

// ---- Handlers ----

// handlePostMessage handles POST /groups/{key}/messages
// Creates a new top-level message or a reply (if parent_message_id is set).
// Returns an HTMX fragment of the new message.
func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	if groupKey == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing group key"})
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	body := r.FormValue("body")
	parentIDStr := r.FormValue("parent_message_id")

	if err := validateString("email", email, 3, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("name", name, 0, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("message", body, 1, 8192); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	var parentID int64
	if parentIDStr != "" {
		parentID, _ = strconv.ParseInt(parentIDStr, 10, 64)
	}

	msg, err := s.store.CreateMessage(groupKey, email, name, body, parentID)
	if err != nil {
		log.Printf("web: create message error: %v", err)
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to post message"})
		return
	}

	// Load reactions (empty for new message)
	reactions, _ := s.store.ListReactions(msg.ID, email)

	s.renderFragment(w, "message_item", messageViewData{
		Message:   msg,
		Reactions: reactions,
		GroupKey:  groupKey,
		CSRFToken: csrfTokenFromRequest(r),
		UserEmail: email,
	})
}

// handleListMessages handles GET /groups/{key}/messages
// Returns an HTMX fragment of the message list (for initial load or pagination).
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	if groupKey == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing group key"})
		return
	}

	cursorStr := r.URL.Query().Get("cursor")
	cursor, _ := strconv.ParseInt(cursorStr, 10, 64)

	messages, hasMore, err := s.store.ListMessages(groupKey, cursor, 20)
	if err != nil {
		log.Printf("web: list messages error: %v", err)
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to load messages"})
		return
	}

	// Load replies and reactions for each message.
	userEmail := strings.TrimSpace(r.URL.Query().Get("email"))
	var views []messageViewData
	var msgIDs []int64
	for _, m := range messages {
		msgIDs = append(msgIDs, m.ID)
	}
	reactionsMap, _ := s.store.GetMessageReactionsMap(msgIDs, userEmail)

	for _, m := range messages {
		replies, _ := s.store.GetReplies(m.ID)
		views = append(views, messageViewData{
			Message:   m,
			Replies:   replies,
			Reactions: reactionsMap[m.ID],
			GroupKey:  groupKey,
			CSRFToken: csrfTokenFromRequest(r),
			UserEmail: userEmail,
		})
	}

	var nextCursor int64
	if hasMore && len(views) > 0 {
		nextCursor = views[len(views)-1].Message.ID
	}

	s.renderFragment(w, "message_list", messageListData{
		Messages:  views,
		GroupKey:  groupKey,
		HasMore:   hasMore,
		Cursor:    nextCursor,
		CSRFToken: csrfTokenFromRequest(r),
		UserEmail: userEmail,
	})
}

// handleSearchMessages handles GET /groups/{key}/messages/search
// Returns an HTMX fragment of search results.
func (s *Server) handleSearchMessages(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	if groupKey == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing group key"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		// Empty search — return all messages (same as list)
		s.handleListMessages(w, r)
		return
	}

	messages, err := s.store.SearchMessages(groupKey, q, 50)
	if err != nil {
		log.Printf("web: search messages error: %v", err)
		s.renderFragment(w, "error_fragment", fragmentData{Error: "search failed"})
		return
	}

	userEmail := strings.TrimSpace(r.URL.Query().Get("email"))
	var msgIDs []int64
	for _, m := range messages {
		msgIDs = append(msgIDs, m.ID)
	}
	reactionsMap, _ := s.store.GetMessageReactionsMap(msgIDs, userEmail)

	var views []messageViewData
	for _, m := range messages {
		replies, _ := s.store.GetReplies(m.ID)
		views = append(views, messageViewData{
			Message:   m,
			Replies:   replies,
			Reactions: reactionsMap[m.ID],
			GroupKey:  groupKey,
			CSRFToken: csrfTokenFromRequest(r),
			UserEmail: userEmail,
		})
	}

	s.renderFragment(w, "message_list", messageListData{
		Messages:  views,
		GroupKey:  groupKey,
		HasMore:   false,
		CSRFToken: csrfTokenFromRequest(r),
		UserEmail: userEmail,
		SearchQ:   q,
	})
}

// handleToggleReaction handles POST /groups/{key}/messages/{id}/reactions
// Adds or removes an emoji reaction on a message (toggle behavior).
func (s *Server) handleToggleReaction(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	messageIDStr := r.PathValue("id")
	if groupKey == "" || messageIDStr == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing parameters"})
		return
	}

	messageID, err := strconv.ParseInt(messageIDStr, 10, 64)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid message id"})
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	emoji := strings.TrimSpace(r.FormValue("emoji"))

	if err := validateString("email", email, 3, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if emoji == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "emoji required"})
		return
	}

	// Check if reaction exists — toggle behavior.
	reactions, _ := s.store.ListReactions(messageID, email)
	alreadyReacted := false
	for _, r2 := range reactions {
		if r2.Emoji == emoji && r2.Reacted {
			alreadyReacted = true
			break
		}
	}

	if alreadyReacted {
		_ = s.store.RemoveReaction(messageID, email, emoji)
	} else {
		_ = s.store.AddReaction(messageID, email, emoji)
	}

	// Return updated reactions for the message.
	updatedReactions, _ := s.store.ListReactions(messageID, email)

	// Get the message for the fragment.
	msg, err := s.store.GetMessage(messageID)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "message not found"})
		return
	}

	replies, _ := s.store.GetReplies(messageID)

	s.renderFragment(w, "message_item", messageViewData{
		Message:   msg,
		Replies:   replies,
		Reactions: updatedReactions,
		GroupKey:  groupKey,
		CSRFToken: csrfTokenFromRequest(r),
		UserEmail: email,
	})
}

// formatMessageTime formats a message timestamp for display.
func formatMessageTime(unix int64) string {
	return formatTime(unix)
}

// formatMessageShort formats a short relative time for messages.
func formatMessageShort(unix int64) string {
	return formatRelative(unix)
}

// formatMessageBody formats message body as safe HTML with linkification.
func formatMessageBody(body string) string {
	return string(linkify(body))
}

// safeHTML returns a template-safe HTML string from a string.
func safeHTML(s string) string {
	return s
}

// initials returns the first letter of a name (uppercased) for avatar display.
func initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	return strings.ToUpper(string(name[0]))
}

// reactionEmojis returns the list of allowed quick-reaction emojis.
func reactionEmojis() []string {
	return []string{"🙏", "✅", "❤️"}
}

// formatInt formats an int64 as a string (for template use).
func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

// _ ensures fmt is used
var _ = fmt.Sprintf