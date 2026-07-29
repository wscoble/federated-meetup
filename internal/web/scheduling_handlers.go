// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- Availability poll handlers ----

// resolveGroupKeyFromName resolves a group canonical name to a group_key.
// Used by scheduling handlers that receive {name} in the route pattern.
func (s *Server) resolveGroupKeyFromName(name string) (string, error) {
	// Use the SQLite cache to resolve canonical name → group_key.
	g, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		return "", err
	}
	return g.GroupKey, nil
}

// handleCreatePoll creates an availability poll for an event.
// The organizer proposes multiple time slots; members vote on their preferred time.
// Form params: proposed_times (repeated YYYY-MM-DDTHH:MM values)
func (s *Server) handleCreatePoll(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("name")
	eventID := r.PathValue("event_id")
	if groupName == "" || eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing event identifier"})
		return
	}

	groupKey, err := s.resolveGroupKeyFromName(groupName)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "group not found"})
		return
	}

	// Collect proposed times from repeated form values.
	// The form sends proposed_times as multiple values.
	r.ParseForm()
	rawTimes := r.Form["proposed_times"]
	if len(rawTimes) == 0 {
		// Also try a single newline-separated field.
		single := r.FormValue("proposed_times_single")
		if single != "" {
			rawTimes = strings.Split(single, "\n")
		}
	}
	if len(rawTimes) == 0 {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "at least one proposed time is required"})
		return
	}

	var proposedTimes []int64
	for _, raw := range rawTimes {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Try multiple formats.
		t, err := time.Parse("2006-01-02T15:04", raw)
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04", raw)
			if err != nil {
				t, err = time.Parse("2006-01-02", raw)
				if err != nil {
					s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid time format: " + raw})
					return
				}
			}
		}
		proposedTimes = append(proposedTimes, t.Unix())
	}

	if len(proposedTimes) == 0 {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "no valid proposed times"})
		return
	}

	poll, err := s.store.CreatePoll(AvailabilityPoll{
		GroupKey:      groupKey,
		EventID:       eventID,
		ProposedTimes: proposedTimes,
	})
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to create poll"})
		return
	}

	log.Printf("web: poll created: id=%d for event %s/%s (%d time slots)", poll.ID, groupKey, eventID, len(proposedTimes))
	s.renderFragment(w, "poll_created_fragment", fragmentData{
		GroupKey: groupKey,
		EventID:  eventID,
	})
}

// handleVoteOnPoll lets a member vote on their preferred time slot.
// Form params: voter_email, voter_name, time_slot_index
func (s *Server) handleVoteOnPoll(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("name")
	eventID := r.PathValue("event_id")
	if groupName == "" || eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing event identifier"})
		return
	}

	groupKey, err := s.resolveGroupKeyFromName(groupName)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "group not found"})
		return
	}

	email := r.FormValue("voter_email")
	name := r.FormValue("voter_name")
	slotIdxStr := r.FormValue("time_slot_index")

	if err := validateString("email", email, 3, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("name", name, 0, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	slotIdx, err := strconv.Atoi(slotIdxStr)
	if err != nil || slotIdx < 0 {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid time slot index"})
		return
	}

	// Get the poll for this event.
	poll, err := s.store.GetPollForEvent(groupKey, eventID)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "no poll found for this event"})
		return
	}

	if slotIdx >= len(poll.ProposedTimes) {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "time slot index out of range"})
		return
	}

	if err := s.store.CastVote(PollVote{
		PollID:        poll.ID,
		VoterEmail:    email,
		VoterName:     name,
		TimeSlotIndex: slotIdx,
	}); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to record vote"})
		return
	}

	log.Printf("web: vote cast: poll=%d email=%s slot=%d", poll.ID, email, slotIdx)
	s.renderFragment(w, "vote_cast_fragment", fragmentData{
		GroupKey: groupKey,
		EventID:  eventID,
	})
}

// ---- Waitlist handlers ----

// handleJoinWaitlist lets a member join the waitlist for a full event.
// Form params: email, name
func (s *Server) handleJoinWaitlist(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("name")
	eventID := r.PathValue("event_id")
	if groupName == "" || eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing event identifier"})
		return
	}

	groupKey, err := s.resolveGroupKeyFromName(groupName)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "group not found"})
		return
	}

	email := r.FormValue("email")
	name := r.FormValue("name")

	if err := validateString("email", email, 3, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("name", name, 0, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	// Verify event exists.
	if _, err := s.eventFromProduct(groupKey, eventID); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "event not found"})
		return
	}

	entry, err := s.store.JoinWaitlist(WaitlistEntry{
		GroupKey: groupKey,
		EventID:  eventID,
		Email:    email,
		Name:     name,
	})
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to join waitlist"})
		return
	}

	count, _ := s.store.WaitlistCount(groupKey, eventID)
	log.Printf("web: joined waitlist: id=%d email=%s event=%s/%s (position %d)", entry.ID, email, groupKey, eventID, count)
	s.renderFragment(w, "waitlist_joined_fragment", fragmentData{
		Email:    email,
		GroupKey: groupKey,
		EventID:  eventID,
	})
}

// handlePromoteFromWaitlist promotes the next person on the waitlist.
// This is an organizer action — requires authentication.
// Form params: (none required — uses path values)
func (s *Server) handlePromoteFromWaitlist(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("name")
	eventID := r.PathValue("event_id")
	if groupName == "" || eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing event identifier"})
		return
	}

	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	// Verify the authenticated group matches the route group.
	resolvedKey, err := s.resolveGroupKeyFromName(groupName)
	if err == nil && resolvedKey != "" && resolvedKey != groupKey {
		// The route name resolves to a different group than the session.
		// Use the resolved key for consistency.
		groupKey = resolvedKey
	}

	if eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "event_id required"})
		return
	}

	entry, err := s.store.PromoteFromWaitlist(groupKey, eventID)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "waitlist is empty or promotion failed"})
		return
	}

	log.Printf("web: promoted from waitlist: email=%s event=%s/%s", entry.Email, groupKey, eventID)
	s.renderFragment(w, "waitlist_promoted_fragment", fragmentData{
		Email:    entry.Email,
		GroupKey: groupKey,
		EventID:  eventID,
	})
}

// ---- Recurrence exception handlers ----

// handleAddRecurrenceException adds an exception date to a recurring event series.
// Form params: recurrence_id, exception_date (YYYY-MM-DD), reason
func (s *Server) handleAddRecurrenceException(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("name")
	if groupName == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "group name required"})
		return
	}

	groupKey, err := s.resolveGroupKeyFromName(groupName)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "group not found"})
		return
	}

	recurrenceID := r.FormValue("recurrence_id")
	exceptionDate := r.FormValue("exception_date")
	reason := r.FormValue("reason")

	if err := validateString("recurrence_id", recurrenceID, 1, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("exception_date", exceptionDate, 10, 10); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "exception_date must be YYYY-MM-DD"})
		return
	}

	// Validate date format.
	if _, err := time.Parse("2006-01-02", exceptionDate); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid date format, use YYYY-MM-DD"})
		return
	}

	if err := validateString("reason", reason, 0, 1024); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	_, err = s.store.AddRecurrenceException(RecurrenceException{
		GroupKey:      groupKey,
		RecurrenceID:  recurrenceID,
		ExceptionDate: exceptionDate,
		Reason:        reason,
	})
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to add exception"})
		return
	}

	log.Printf("web: recurrence exception added: group=%s recurrence=%s date=%s reason=%s", groupKey, recurrenceID, exceptionDate, reason)
	s.renderFragment(w, "exception_added_fragment", fragmentData{
		GroupKey: groupKey,
		EventID:  recurrenceID,
	})
}