// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- Store-level tests for scheduling features ----

func TestStoreCreatePoll(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	fixedTime := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	poll, err := store.CreatePoll(AvailabilityPoll{
		GroupKey:      "g1",
		EventID:       "e1",
		ProposedTimes: []int64{1000, 2000, 3000},
	})
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	if poll.ID == 0 {
		t.Fatal("poll ID should be non-zero")
	}
	if len(poll.ProposedTimes) != 3 {
		t.Fatalf("expected 3 proposed times, got %d", len(poll.ProposedTimes))
	}
	if poll.CreatedAt != fixedTime.Unix() {
		t.Fatalf("expected CreatedAt=%d, got %d", fixedTime.Unix(), poll.CreatedAt)
	}

	// Retrieve it.
	got, err := store.GetPollForEvent("g1", "e1")
	if err != nil {
		t.Fatalf("GetPollForEvent: %v", err)
	}
	if got.ID != poll.ID {
		t.Fatalf("expected poll ID %d, got %d", poll.ID, got.ID)
	}
	if len(got.ProposedTimes) != 3 {
		t.Fatalf("expected 3 proposed times, got %d", len(got.ProposedTimes))
	}
}

func TestStoreCreatePollNoTimes(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	_, err = store.CreatePoll(AvailabilityPoll{
		GroupKey:      "g1",
		EventID:       "e1",
		ProposedTimes: []int64{},
	})
	if err == nil {
		t.Fatal("expected error for empty proposed times")
	}
}

func TestStoreCastVoteAndList(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	fixedTime := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	poll, err := store.CreatePoll(AvailabilityPoll{
		GroupKey:      "g1",
		EventID:       "e1",
		ProposedTimes: []int64{1000, 2000},
	})
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}

	// Alice votes for slot 0.
	if err := store.CastVote(PollVote{
		PollID:        poll.ID,
		VoterEmail:    "alice@example.com",
		VoterName:     "Alice",
		TimeSlotIndex: 0,
	}); err != nil {
		t.Fatalf("CastVote: %v", err)
	}

	// Bob votes for slot 0 and slot 1.
	if err := store.CastVote(PollVote{
		PollID:        poll.ID,
		VoterEmail:    "bob@example.com",
		VoterName:     "Bob",
		TimeSlotIndex: 0,
	}); err != nil {
		t.Fatalf("CastVote: %v", err)
	}
	if err := store.CastVote(PollVote{
		PollID:        poll.ID,
		VoterEmail:    "bob@example.com",
		VoterName:     "Bob",
		TimeSlotIndex: 1,
	}); err != nil {
		t.Fatalf("CastVote: %v", err)
	}

	// Alice re-votes slot 0 (upsert — should not duplicate).
	if err := store.CastVote(PollVote{
		PollID:        poll.ID,
		VoterEmail:    "alice@example.com",
		VoterName:     "Alice",
		TimeSlotIndex: 0,
	}); err != nil {
		t.Fatalf("CastVote (upsert): %v", err)
	}

	votes, err := store.ListVotesForPoll(poll.ID)
	if err != nil {
		t.Fatalf("ListVotesForPoll: %v", err)
	}
	if len(votes) != 3 {
		t.Fatalf("expected 3 votes, got %d", len(votes))
	}

	// Slot 0 should have 2 votes, slot 1 should have 1.
	count0, _ := store.VoteCountForSlot(poll.ID, 0)
	if count0 != 2 {
		t.Fatalf("expected 2 votes for slot 0, got %d", count0)
	}
	count1, _ := store.VoteCountForSlot(poll.ID, 1)
	if count1 != 1 {
		t.Fatalf("expected 1 vote for slot 1, got %d", count1)
	}
}

func TestStoreWaitlistJoinAndPromote(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	fixedTime := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixedTime })

	// Alice joins first.
	entry1, err := store.JoinWaitlist(WaitlistEntry{
		GroupKey: "g1",
		EventID:  "e1",
		Email:    "alice@example.com",
		Name:     "Alice",
	})
	if err != nil {
		t.Fatalf("JoinWaitlist: %v", err)
	}
	if entry1.ID == 0 {
		t.Fatal("entry ID should be non-zero")
	}

	// Bob joins second.
	_, err = store.JoinWaitlist(WaitlistEntry{
		GroupKey: "g1",
		EventID:  "e1",
		Email:    "bob@example.com",
		Name:     "Bob",
	})
	if err != nil {
		t.Fatalf("JoinWaitlist: %v", err)
	}

	// Count should be 2.
	count, _ := store.WaitlistCount("g1", "e1")
	if count != 2 {
		t.Fatalf("expected waitlist count 2, got %d", count)
	}

	// List should return in join order.
	list, err := store.ListWaitlist("g1", "e1")
	if err != nil {
		t.Fatalf("ListWaitlist: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].Email != "alice@example.com" {
		t.Fatalf("expected Alice first, got %s", list[0].Email)
	}

	// Promote — should get Alice (first to join).
	promoted, err := store.PromoteFromWaitlist("g1", "e1")
	if err != nil {
		t.Fatalf("PromoteFromWaitlist: %v", err)
	}
	if promoted.Email != "alice@example.com" {
		t.Fatalf("expected Alice promoted, got %s", promoted.Email)
	}
	if promoted.NotifiedAt == 0 {
		t.Fatal("promoted entry should have non-zero notified_at")
	}

	// After promotion, count should still be 2 (we don't delete on promote).
	count, _ = store.WaitlistCount("g1", "e1")
	if count != 2 {
		t.Fatalf("expected waitlist count still 2 after promotion, got %d", count)
	}
}

func TestStorePromoteEmptyWaitlist(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	_, err = store.PromoteFromWaitlist("g1", "e1")
	if err == nil {
		t.Fatal("expected error promoting from empty waitlist")
	}
}

func TestStoreRecurrenceExceptions(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Add an exception.
	exc, err := store.AddRecurrenceException(RecurrenceException{
		GroupKey:      "g1",
		RecurrenceID: "rec-001",
		ExceptionDate: "2026-08-01",
		Reason:        "Holiday",
	})
	if err != nil {
		t.Fatalf("AddRecurrenceException: %v", err)
	}
	if exc.ID == 0 {
		t.Fatal("exception ID should be non-zero")
	}

	// List exceptions.
	list, err := store.ListRecurrenceExceptions("g1", "rec-001")
	if err != nil {
		t.Fatalf("ListRecurrenceExceptions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 exception, got %d", len(list))
	}
	if list[0].ExceptionDate != "2026-08-01" {
		t.Fatalf("expected date 2026-08-01, got %s", list[0].ExceptionDate)
	}
	if list[0].Reason != "Holiday" {
		t.Fatalf("expected reason 'Holiday', got %s", list[0].Reason)
	}

	// Check if date is excepted.
	excepted, err := store.IsDateExcepted("g1", "rec-001", "2026-08-01")
	if err != nil {
		t.Fatalf("IsDateExcepted: %v", err)
	}
	if !excepted {
		t.Fatal("expected 2026-08-01 to be excepted")
	}

	// Non-exception date.
	excepted, _ = store.IsDateExcepted("g1", "rec-001", "2026-09-01")
	if excepted {
		t.Fatal("expected 2026-09-01 to NOT be excepted")
	}
}

func TestStoreAddRecurrenceExceptionValidation(t *testing.T) {
	store, err := NewStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Empty recurrence_id should fail.
	_, err = store.AddRecurrenceException(RecurrenceException{
		GroupKey:      "g1",
		RecurrenceID:  "",
		ExceptionDate: "2026-08-01",
	})
	if err == nil {
		t.Fatal("expected error for empty recurrence_id")
	}

	// Empty exception_date should fail.
	_, err = store.AddRecurrenceException(RecurrenceException{
		GroupKey:      "g1",
		RecurrenceID:  "rec-001",
		ExceptionDate: "",
	})
	if err == nil {
		t.Fatal("expected error for empty exception_date")
	}
}

// ---- HTTP handler tests ----

func TestHTTPCreatePoll(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, eventID := seedTestData(t, srv)

	// Get canonical name from the seeded group.
	group, _ := srv.store.GetGroup(groupKey)
	canonicalName := group.CanonicalName

	form := strings.NewReader("proposed_times=2026-08-01T18:00&proposed_times=2026-08-08T18:00&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+canonicalName+"/events/"+eventID+"/poll", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("create poll should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "poll") {
		t.Fatalf("response should mention poll, got: %s", body)
	}

	// Verify poll was stored.
	poll, err := srv.store.GetPollForEvent(groupKey, eventID)
	if err != nil {
		t.Fatalf("GetPollForEvent: %v", err)
	}
	if len(poll.ProposedTimes) != 2 {
		t.Fatalf("expected 2 proposed times, got %d", len(poll.ProposedTimes))
	}
}

func TestHTTPCreatePollNoTimes(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	form := strings.NewReader("csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/poll", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("should return 200 with error fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "proposed time") {
		t.Fatalf("response should mention 'proposed time', got: %s", body)
	}
}

func TestHTTPVoteOnPoll(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup(groupKey)

	// Create a poll first.
	poll, err := srv.store.CreatePoll(AvailabilityPoll{
		GroupKey:      groupKey,
		EventID:       eventID,
		ProposedTimes: []int64{1000, 2000},
	})
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	_ = poll

	form := strings.NewReader("voter_email=alice@example.com&voter_name=Alice&time_slot_index=0&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/poll/vote", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("vote should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Vote") {
		t.Fatalf("response should mention Vote, got: %s", body)
	}

	// Verify vote was stored.
	poll2, _ := srv.store.GetPollForEvent(groupKey, eventID)
	count, _ := srv.store.VoteCountForSlot(poll2.ID, 0)
	if count != 1 {
		t.Fatalf("expected 1 vote for slot 0, got %d", count)
	}
}

func TestHTTPVoteOnPollInvalidSlot(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup(groupKey)

	// Create a poll with 2 slots.
	_, err := srv.store.CreatePoll(AvailabilityPoll{
		GroupKey:      groupKey,
		EventID:       eventID,
		ProposedTimes: []int64{1000, 2000},
	})
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}

	// Vote for out-of-range slot 5.
	form := strings.NewReader("voter_email=alice@example.com&voter_name=Alice&time_slot_index=5&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/poll/vote", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("should return 200 with error fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "out of range") {
		t.Fatalf("response should mention 'out of range', got: %s", body)
	}
}

func TestHTTPVoteOnPollNoPollExists(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	form := strings.NewReader("voter_email=alice@example.com&voter_name=Alice&time_slot_index=0&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/poll/vote", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("should return 200 with error fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "no poll") {
		t.Fatalf("response should mention 'no poll', got: %s", body)
	}
}

func TestHTTPJoinWaitlist(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup(groupKey)

	form := strings.NewReader("email=alice@example.com&name=Alice&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/waitlist/join", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("join waitlist should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "waitlist") {
		t.Fatalf("response should mention waitlist, got: %s", body)
	}

	// Verify entry was stored.
	count, _ := srv.store.WaitlistCount(groupKey, eventID)
	if count != 1 {
		t.Fatalf("expected 1 waitlist entry, got %d", count)
	}
}

func TestHTTPJoinWaitlistInvalidEmail(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	form := strings.NewReader("email=x&name=Alice&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/waitlist/join", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("should return 200 with error fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "too short") {
		t.Fatalf("response should mention 'too short', got: %s", body)
	}
}

func TestHTTPPromoteWaitlist(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup(groupKey)

	// Add someone to waitlist.
	_, err := srv.store.JoinWaitlist(WaitlistEntry{
		GroupKey: groupKey,
		EventID:  eventID,
		Email:    "alice@example.com",
		Name:     "Alice",
	})
	if err != nil {
		t.Fatalf("JoinWaitlist: %v", err)
	}

	// Need to set up an organizer session for promote.
	srv.store.CreateSession("test-session", groupKey, 24*time.Hour)

	form := strings.NewReader("csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/waitlist/promote", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "test-session"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("promote should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Promoted") {
		t.Fatalf("response should mention Promoted, got: %s", body)
	}
}

func TestHTTPPromoteWaitlistUnauthenticated(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	form := strings.NewReader("csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/waitlist/promote", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	// Should redirect to login (303).
	if w.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated promote should redirect (303), got %d", w.Code)
	}
}

func TestHTTPAddRecurrenceException(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	groupKey, _ := seedTestData(t, srv)
	group, _ := srv.store.GetGroup(groupKey)

	form := strings.NewReader("recurrence_id=rec-001&exception_date=2026-08-01&reason=Holiday&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/evt-001/exceptions", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("add exception should return 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "exception") {
		t.Fatalf("response should mention exception, got: %s", body)
	}

	// Verify it was stored.
	list, err := srv.store.ListRecurrenceExceptions(groupKey, "rec-001")
	if err != nil {
		t.Fatalf("ListRecurrenceExceptions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 exception, got %d", len(list))
	}
	if list[0].ExceptionDate != "2026-08-01" {
		t.Fatalf("expected date 2026-08-01, got %s", list[0].ExceptionDate)
	}
}

func TestHTTPAddRecurrenceExceptionInvalidDate(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	form := strings.NewReader("recurrence_id=rec-001&exception_date=invalid&reason=&csrf_token=fake")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/evt-001/exceptions", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "fake"})
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("should return 200 with error fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "date") && !strings.Contains(body, "format") {
		t.Fatalf("response should mention date/format error, got: %s", body)
	}
}

func TestHTTPPollCSRFValidation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	_, eventID := seedTestData(t, srv)
	group, _ := srv.store.GetGroup("testgroup123")

	// POST without matching CSRF token should return 403.
	form := strings.NewReader("proposed_times=2026-08-01T18:00")
	req := httptest.NewRequest("POST", "/groups/"+group.CanonicalName+"/events/"+eventID+"/poll", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No CSRF cookie set.
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("POST without CSRF token should return 403, got %d", w.Code)
	}
}