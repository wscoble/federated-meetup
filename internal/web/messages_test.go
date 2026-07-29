// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- Store tests for messaging ----

func TestStoreCreateMessage(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	fixedTime := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	msg, err := store.CreateMessage("g1", "alice@example.com", "Alice", "Hello world!", 0)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if msg.ID == 0 {
		t.Fatal("expected non-zero message ID")
	}
	if msg.GroupKey != "g1" {
		t.Fatalf("expected group_key g1, got %s", msg.GroupKey)
	}
	if msg.Body != "Hello world!" {
		t.Fatalf("expected body 'Hello world!', got %s", msg.Body)
	}
	if msg.CreatedAt != fixedTime.Unix() {
		t.Fatalf("expected created_at %d, got %d", fixedTime.Unix(), msg.CreatedAt)
	}
	if msg.ParentMessageID.Valid {
		t.Fatal("expected parent_message_id to be NULL for top-level message")
	}
}

func TestStoreCreateMessageValidation(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Empty body
	_, err = store.CreateMessage("g1", "alice@example.com", "Alice", "", 0)
	if err == nil {
		t.Fatal("expected error for empty body")
	}

	// Empty group key
	_, err = store.CreateMessage("", "alice@example.com", "Alice", "Hello", 0)
	if err == nil {
		t.Fatal("expected error for empty group_key")
	}

	// Empty email
	_, err = store.CreateMessage("g1", "", "Alice", "Hello", 0)
	if err == nil {
		t.Fatal("expected error for empty email")
	}

	// Body too long
	longBody := strings.Repeat("a", 8193)
	_, err = store.CreateMessage("g1", "alice@example.com", "Alice", longBody, 0)
	if err == nil {
		t.Fatal("expected error for body too long")
	}
}

func TestStoreCreateReply(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create parent message
	parent, err := store.CreateMessage("g1", "alice@example.com", "Alice", "Parent message", 0)
	if err != nil {
		t.Fatalf("CreateMessage parent: %v", err)
	}

	// Create reply
	reply, err := store.CreateMessage("g1", "bob@example.com", "Bob", "Reply message", parent.ID)
	if err != nil {
		t.Fatalf("CreateMessage reply: %v", err)
	}
	if !reply.ParentMessageID.Valid {
		t.Fatal("expected parent_message_id to be set for reply")
	}
	if reply.ParentMessageID.Int64 != parent.ID {
		t.Fatalf("expected parent_message_id %d, got %d", parent.ID, reply.ParentMessageID.Int64)
	}
}

func TestStoreListMessages(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create 5 top-level messages
	for i := 0; i < 5; i++ {
		_, err := store.CreateMessage("g1", "alice@example.com", "Alice", "Message "+string(rune('A'+i)), 0)
		if err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
	}

	// Create a reply (should not appear in top-level list)
	parent, _ := store.CreateMessage("g1", "alice@example.com", "Alice", "Parent", 0)
	_, _ = store.CreateMessage("g1", "bob@example.com", "Bob", "Reply", parent.ID)

	// List all top-level messages
	messages, hasMore, err := store.ListMessages("g1", 0, 20)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	// Should be 6 top-level messages (5 + the parent)
	if len(messages) != 6 {
		t.Fatalf("expected 6 top-level messages, got %d", len(messages))
	}
	if hasMore {
		t.Fatal("expected hasMore=false for 6 messages with limit 20")
	}

	// Verify newest-first ordering
	if messages[0].Body != "Parent" {
		t.Fatalf("expected newest message first, got %s", messages[0].Body)
	}
}

func TestStoreListMessagesPagination(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create 25 messages
	var lastID int64
	for i := 0; i < 25; i++ {
		msg, err := store.CreateMessage("g1", "alice@example.com", "Alice", "Message", 0)
		if err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
		lastID = msg.ID
	}

	// First page (limit 10)
	page1, hasMore1, err := store.ListMessages("g1", 0, 10)
	if err != nil {
		t.Fatalf("ListMessages page1: %v", err)
	}
	if len(page1) != 10 {
		t.Fatalf("expected 10 messages in page1, got %d", len(page1))
	}
	if !hasMore1 {
		t.Fatal("expected hasMore=true for page1")
	}

	// Second page using cursor (last ID from page1)
	cursor := page1[len(page1)-1].ID
	page2, hasMore2, err := store.ListMessages("g1", cursor, 10)
	if err != nil {
		t.Fatalf("ListMessages page2: %v", err)
	}
	if len(page2) != 10 {
		t.Fatalf("expected 10 messages in page2, got %d", len(page2))
	}
	if !hasMore2 {
		t.Fatal("expected hasMore=true for page2")
	}

	// Third page
	cursor2 := page2[len(page2)-1].ID
	page3, hasMore3, err := store.ListMessages("g1", cursor2, 10)
	if err != nil {
		t.Fatalf("ListMessages page3: %v", err)
	}
	if len(page3) != 5 {
		t.Fatalf("expected 5 messages in page3, got %d", len(page3))
	}
	if hasMore3 {
		t.Fatal("expected hasMore=false for page3")
	}

	// Ensure no overlap between pages
	if page1[0].ID == lastID {
		// page1[0] should be the newest (ID = lastID)
	}
	// Verify page ordering: page1 IDs > page2 IDs > page3 IDs
	if page1[9].ID <= page2[0].ID {
		t.Fatal("page1 last ID should be greater than page2 first ID")
	}
	if page2[9].ID <= page3[0].ID {
		t.Fatal("page2 last ID should be greater than page3 first ID")
	}
}

func TestStoreGetMessageThread(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create parent
	parent, _ := store.CreateMessage("g1", "alice@example.com", "Alice", "Thread parent", 0)

	// Create 3 replies
	for i := 0; i < 3; i++ {
		_, err := store.CreateMessage("g1", "bob@example.com", "Bob", "Reply "+string(rune('1'+i)), parent.ID)
		if err != nil {
			t.Fatalf("CreateMessage reply %d: %v", i, err)
		}
	}

	// Get the thread
	msg, replies, err := store.GetMessageThread(parent.ID)
	if err != nil {
		t.Fatalf("GetMessageThread: %v", err)
	}
	if msg.ID != parent.ID {
		t.Fatalf("expected parent ID %d, got %d", parent.ID, msg.ID)
	}
	if len(replies) != 3 {
		t.Fatalf("expected 3 replies, got %d", len(replies))
	}
}

func TestStoreGetReplies(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	parent, _ := store.CreateMessage("g1", "alice@example.com", "Alice", "Parent", 0)
	_, _ = store.CreateMessage("g1", "bob@example.com", "Bob", "Reply 1", parent.ID)
	_, _ = store.CreateMessage("g1", "carol@example.com", "Carol", "Reply 2", parent.ID)

	replies, err := store.GetReplies(parent.ID)
	if err != nil {
		t.Fatalf("GetReplies: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("expected 2 replies, got %d", len(replies))
	}
	// Should be ordered by ID ASC
	if replies[0].Body != "Reply 1" {
		t.Fatalf("expected first reply 'Reply 1', got %s", replies[0].Body)
	}
}

func TestStoreSearchMessages(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	_, _ = store.CreateMessage("g1", "alice@example.com", "Alice", "Praying for healing", 0)
	_, _ = store.CreateMessage("g1", "bob@example.com", "Bob", "See you at the meeting", 0)
	_, _ = store.CreateMessage("g1", "carol@example.com", "Carol", "Praying for guidance", 0)
	_, _ = store.CreateMessage("g2", "dave@example.com", "Dave", "Praying in wrong group", 0)

	// Search for "praying" in g1
	results, err := store.SearchMessages("g1", "praying", 20)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	// FTS5 or LIKE should find 2 messages in g1
	if len(results) != 2 {
		t.Fatalf("expected 2 search results for 'praying' in g1, got %d", len(results))
	}

	// Search for "meeting"
	results2, err := store.SearchMessages("g1", "meeting", 20)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results2) != 1 {
		t.Fatalf("expected 1 search result for 'meeting', got %d", len(results2))
	}

	// Empty query returns nil
	results3, err := store.SearchMessages("g1", "", 20)
	if err != nil {
		t.Fatalf("SearchMessages empty: %v", err)
	}
	if results3 != nil {
		t.Fatal("expected nil for empty search query")
	}
}

func TestStoreReactions(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	msg, _ := store.CreateMessage("g1", "alice@example.com", "Alice", "Test message", 0)

	// Add reactions
	if err := store.AddReaction(msg.ID, "bob@example.com", "🙏"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if err := store.AddReaction(msg.ID, "carol@example.com", "🙏"); err != nil {
		t.Fatalf("AddReaction 2: %v", err)
	}
	if err := store.AddReaction(msg.ID, "bob@example.com", "❤️"); err != nil {
		t.Fatalf("AddReaction 3: %v", err)
	}

	// Idempotent — adding same reaction should not error
	if err := store.AddReaction(msg.ID, "bob@example.com", "🙏"); err != nil {
		t.Fatalf("AddReaction idempotent: %v", err)
	}

	// List reactions (from bob's perspective)
	reactions, err := store.ListReactions(msg.ID, "bob@example.com")
	if err != nil {
		t.Fatalf("ListReactions: %v", err)
	}
	if len(reactions) != 2 {
		t.Fatalf("expected 2 reaction summaries, got %d", len(reactions))
	}

	// Find 🙏 reaction
	var foundPraying *ReactionSummary
	for i := range reactions {
		if reactions[i].Emoji == "🙏" {
			foundPraying = &reactions[i]
			break
		}
	}
	if foundPraying == nil {
		t.Fatal("expected to find 🙏 reaction")
	}
	if foundPraying.Count != 2 {
		t.Fatalf("expected count 2 for 🙏, got %d", foundPraying.Count)
	}
	if !foundPraying.Reacted {
		t.Fatal("expected Reacted=true for bob on 🙏")
	}

	// Remove reaction
	if err := store.RemoveReaction(msg.ID, "bob@example.com", "🙏"); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	reactions2, _ := store.ListReactions(msg.ID, "bob@example.com")
	for _, r := range reactions2 {
		if r.Emoji == "🙏" {
			if r.Count != 1 {
				t.Fatalf("expected count 1 for 🙏 after removal, got %d", r.Count)
			}
			if r.Reacted {
				t.Fatal("expected Reacted=false for bob on 🙏 after removal")
			}
		}
	}
}

func TestStoreGetMessageReactionsMap(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	msg1, _ := store.CreateMessage("g1", "alice@example.com", "Alice", "Message 1", 0)
	msg2, _ := store.CreateMessage("g1", "bob@example.com", "Bob", "Message 2", 0)

	_ = store.AddReaction(msg1.ID, "carol@example.com", "🙏")
	_ = store.AddReaction(msg2.ID, "carol@example.com", "❤️")
	_ = store.AddReaction(msg2.ID, "dave@example.com", "❤️")

	map1, err := store.GetMessageReactionsMap([]int64{msg1.ID, msg2.ID}, "carol@example.com")
	if err != nil {
		t.Fatalf("GetMessageReactionsMap: %v", err)
	}
	if len(map1) != 2 {
		t.Fatalf("expected 2 entries in reactions map, got %d", len(map1))
	}
	if len(map1[msg1.ID]) != 1 {
		t.Fatalf("expected 1 reaction for msg1, got %d", len(map1[msg1.ID]))
	}
	if len(map1[msg2.ID]) != 1 {
		t.Fatalf("expected 1 reaction type for msg2, got %d", len(map1[msg2.ID]))
	}
	if map1[msg2.ID][0].Count != 2 {
		t.Fatalf("expected count 2 for ❤️ on msg2, got %d", map1[msg2.ID][0].Count)
	}
}

func TestStoreCountMessages(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	_, _ = store.CreateMessage("g1", "alice@example.com", "Alice", "Msg 1", 0)
	_, _ = store.CreateMessage("g1", "bob@example.com", "Bob", "Msg 2", 0)
	_, _ = store.CreateMessage("g2", "carol@example.com", "Carol", "Msg 3", 0)

	count1, _ := store.CountMessages("g1")
	if count1 != 2 {
		t.Fatalf("expected 2 messages in g1, got %d", count1)
	}

	count2, _ := store.CountMessages("g2")
	if count2 != 1 {
		t.Fatalf("expected 1 message in g2, got %d", count2)
	}

	count3, _ := store.CountMessages("g3")
	if count3 != 0 {
		t.Fatalf("expected 0 messages in g3, got %d", count3)
	}
}

func TestFTSEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{"hello world", `"hello" "world"`},
		{`say "hi"`, `"say" """hi"""`},
		{"", ``},
	}
	for _, tt := range tests {
		got := ftsEscape(tt.input)
		if got != tt.want {
			t.Errorf("ftsEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---- Handler tests for messaging ----

func TestPostMessage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	form := strings.NewReader("email=alice@example.com&name=Alice&body=Hello+group&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+groupKey+"/messages", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Hello group") {
		t.Fatalf("response should contain message body, got: %s", body)
	}
	if !strings.Contains(body, "Alice") {
		t.Fatal("response should contain author name")
	}
}

func TestPostMessageReply(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// First, create a top-level message
	parent, err := srv.store.CreateMessage(groupKey, "alice@example.com", "Alice", "Parent message", 0)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Post a reply via HTTP
	form := strings.NewReader("email=bob@example.com&name=Bob&body=Reply+to+Alice&parent_message_id="+strconvInt64(parent.ID)+"&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+groupKey+"/messages", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify reply was stored
	replies, err := srv.store.GetReplies(parent.ID)
	if err != nil {
		t.Fatalf("GetReplies: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}
	if replies[0].Body != "Reply to Alice" {
		t.Fatalf("expected reply body 'Reply to Alice', got %s", replies[0].Body)
	}
}

func TestPostMessageValidation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Empty body
	form := strings.NewReader("email=alice@example.com&name=Alice&body=&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+groupKey+"/messages", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 (error fragment), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "too short") && !strings.Contains(body, "empty") {
		t.Fatalf("expected validation error message, got: %s", body)
	}
}

func TestListMessagesHTMX(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Create some messages
	for i := 0; i < 3; i++ {
		_, err := srv.store.CreateMessage(groupKey, "alice@example.com", "Alice", "Message "+strconvInt(i), 0)
		if err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	req := httptest.NewRequest("GET", "/groups/"+groupKey+"/messages", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "message-item") {
		t.Fatal("response should contain message-item class")
	}
	if !strings.Contains(body, "Message 0") {
		t.Fatal("response should contain message bodies")
	}
}

func TestSearchMessagesHTMX(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	_, _ = srv.store.CreateMessage(groupKey, "alice@example.com", "Alice", "Prayer meeting tonight", 0)
	_, _ = srv.store.CreateMessage(groupKey, "bob@example.com", "Bob", "Game night Friday", 0)

	req := httptest.NewRequest("GET", "/groups/"+groupKey+"/messages/search?q=prayer", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Prayer meeting") {
		t.Fatalf("search results should contain 'Prayer meeting', got: %s", body)
	}
	if strings.Contains(body, "Game night") {
		t.Fatal("search results should not contain 'Game night'")
	}
}

func TestToggleReaction(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	msg, err := srv.store.CreateMessage(groupKey, "alice@example.com", "Alice", "Test message", 0)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Add reaction
	form := strings.NewReader("email=bob@example.com&emoji=🙏&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+groupKey+"/messages/"+strconvInt64(msg.ID)+"/reactions", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify reaction was stored
	reactions, _ := srv.store.ListReactions(msg.ID, "bob@example.com")
	if len(reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %d", len(reactions))
	}
	if reactions[0].Emoji != "🙏" {
		t.Fatalf("expected emoji 🙏, got %s", reactions[0].Emoji)
	}
	if reactions[0].Count != 1 {
		t.Fatalf("expected count 1, got %d", reactions[0].Count)
	}

	// Toggle (remove) reaction
	form2 := strings.NewReader("email=bob@example.com&emoji=🙏&csrf_token=fake")
	req2 := httptest.NewRequest("POST", "/groups/"+groupKey+"/messages/"+strconvInt64(msg.ID)+"/reactions", form2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("expected 200 on toggle, got %d", w2.Code)
	}

	// Verify reaction was removed
	reactions2, _ := srv.store.ListReactions(msg.ID, "bob@example.com")
	for _, r := range reactions2 {
		if r.Emoji == "🙏" && r.Reacted {
			t.Fatal("reaction should be removed after toggle")
		}
	}
}

func TestGroupPageWithMessages(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	groupKey, _ := seedTestData(t, srv)

	// Create messages
	_, _ = srv.store.CreateMessage(groupKey, "alice@example.com", "Alice", "Welcome to the group!", 0)
	_, _ = srv.store.CreateMessage(groupKey, "bob@example.com", "Bob", "Thanks for having us", 0)

	req := httptest.NewRequest("GET", "/groups/test-group", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Group Chat") {
		t.Fatal("group page should contain 'Group Chat' section")
	}
	if !strings.Contains(body, "Welcome to the group!") {
		t.Fatal("group page should contain messages")
	}
	if !strings.Contains(body, "message-compose") {
		t.Fatal("group page should contain compose form")
	}
}

func TestGroupPageEmptyMessages(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	seedTestData(t, srv)

	req := httptest.NewRequest("GET", "/groups/test-group", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Group Chat") {
		t.Fatal("group page should contain 'Group Chat' section even with no messages")
	}
	if !strings.Contains(body, "No messages yet") {
		t.Fatal("group page should show empty state for messages")
	}
}

// ---- Helpers ----

func strconvInt64(v int64) string {
	return strconvInt(int(v))
}

func strconvInt(v int) string {
	if v == 0 {
		return "0"
	}
	if v < 0 {
		return "-" + strconvInt(-v)
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}