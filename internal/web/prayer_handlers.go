// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- Prayer request data types for templates ----

type prayerListData struct {
	pageBase
	Group           CachedGroup
	PrayerRequests  []PrayerRequestWithUpdates
	IsLeader        bool
	StaleRequests   []PrayerRequest
	PrayerCount     int
}

type prayerPrintData struct {
	Group          CachedGroup
	PrayerRequests []PrayerRequestWithUpdates
	GeneratedAt    int64
}

type prayerFragmentData struct {
	PrayerRequest PrayerRequestWithUpdates
	IsLeader      bool
	GroupCanonical string
	CSRFToken     string
}

type prayerErrorData struct {
	Error string
}

// ---- Prayer request handlers ----

// handlePrayerList renders the prayer request section for a group page.
// This is the full group page with the prayer section embedded.
func (s *Server) handlePrayerList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "group name required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	prayers, _ := s.store.GetPrayerRequestsWithUpdates(group.GroupKey)
	count, _ := s.store.CountPrayerRequests(group.GroupKey)

	// Check if the viewer is an authenticated leader for this group.
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	var stale []PrayerRequest
	if isLeader {
		stale, _ = s.store.StalePrayerRequests(group.GroupKey, 30)
	}

	s.renderPage(w, "prayer_list", prayerListData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc:  "Prayer requests for " + group.DisplayName,
		},
		Group:          group,
		PrayerRequests: prayers,
		IsLeader:       isLeader,
		StaleRequests:   stale,
		PrayerCount:     count,
	})
}

// handlePrayerSubmit handles the HTMX POST to create a new prayer request.
// Returns an HTMX fragment that swaps into the prayer list.
func (s *Server) handlePrayerSubmit(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "missing group name"})
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "group not found"})
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	category := strings.TrimSpace(r.FormValue("category"))
	email := strings.TrimSpace(r.FormValue("email"))
	submitterName := strings.TrimSpace(r.FormValue("submitter_name"))
	anonymous := r.FormValue("is_anonymous") == "on" || r.FormValue("is_anonymous") == "1"

	// Validate
	if err := validateString("title", title, 1, 256); err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: err.Error()})
		return
	}
	if err := validateString("body", body, 0, 4096); err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: err.Error()})
		return
	}
	if err := validateString("email", email, 0, 256); err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: err.Error()})
		return
	}
	if !validatePrayerCategory(category) {
		category = PrayerCategoryGeneral
	}

	pr := PrayerRequest{
		GroupKey:         group.GroupKey,
		SubmittedByEmail: email,
		SubmittedByName:  submitterName,
		Title:            title,
		Body:             body,
		Category:         category,
		Status:           PrayerStatusActive,
		IsAnonymous:      anonymous,
	}

	id, err := s.store.CreatePrayerRequest(pr)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "failed to create prayer request"})
		return
	}
	pr.ID = id

	// Check leader status for display
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	s.renderFragment(w, "prayer_item", prayerFragmentData{
		PrayerRequest: PrayerRequestWithUpdates{
			PrayerRequest: pr,
			Updates:       nil,
		},
		IsLeader:      isLeader,
		GroupCanonical: group.CanonicalName,
		CSRFToken:     csrfTokenFromRequest(r),
	})
}

// handlePrayerUpdate handles the HTMX POST to add an update to a prayer request.
// Returns an HTMX fragment with the updated request + timeline.
func (s *Server) handlePrayerUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	idStr := r.PathValue("id")
	if name == "" || idStr == "" {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "missing parameters"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "invalid prayer request ID"})
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "group not found"})
		return
	}

	pr, err := s.store.GetPrayerRequest(id)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "prayer request not found"})
		return
	}

	// Verify the prayer request belongs to this group (group-scoped).
	if pr.GroupKey != group.GroupKey {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "prayer request not found"})
		return
	}

	updateText := strings.TrimSpace(r.FormValue("update_text"))
	statusChange := strings.TrimSpace(r.FormValue("status_change"))
	createdBy := strings.TrimSpace(r.FormValue("created_by"))

	if err := validateString("update", updateText, 1, 4096); err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: err.Error()})
		return
	}

	// Validate status change if provided.
	if statusChange != "" && statusChange != "none" && !validatePrayerStatus(statusChange) {
		statusChange = ""
	}

	_, err = s.store.CreatePrayerUpdate(PrayerUpdate{
		PrayerRequestID: id,
		UpdateText:      updateText,
		StatusChange:    statusChange,
		CreatedBy:       createdBy,
	})
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "failed to add update"})
		return
	}

	// Reload the prayer request with updates.
	prUpdated, err := s.store.GetPrayerRequest(id)
	if err != nil {
		s.renderFragment(w, "prayer_error", prayerErrorData{Error: "failed to reload"})
		return
	}
	updates, _ := s.store.ListPrayerUpdates(id)

	// Check leader status
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	s.renderFragment(w, "prayer_item", prayerFragmentData{
		PrayerRequest: PrayerRequestWithUpdates{
			PrayerRequest: prUpdated,
			Updates:       updates,
		},
		IsLeader:      isLeader,
		GroupCanonical: group.CanonicalName,
		CSRFToken:     csrfTokenFromRequest(r),
	})
}

// handlePrayerPrint renders a clean printable view of all prayer requests
// for a group, suitable for use during meetings.
func (s *Server) handlePrayerPrint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "group name required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	prayers, _ := s.store.GetPrayerRequestsWithUpdates(group.GroupKey)

	// Render a standalone printable page (no base template chrome).
	t, ok := s.tmpls["prayer_print"]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := prayerPrintData{
		Group:          group,
		PrayerRequests: prayers,
		GeneratedAt:    s.now().Unix(),
	}
	if err := t.ExecuteTemplate(w, "prayer_print", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// ---- Prayer request template helpers ----

// prayerCategoryLabel returns a human-readable label for a prayer category.
func prayerCategoryLabel(cat string) string {
	switch cat {
	case PrayerCategoryGeneral:
		return "General"
	case PrayerCategoryHealing:
		return "Healing"
	case PrayerCategoryFamily:
		return "Family"
	case PrayerCategoryWork:
		return "Work"
	case PrayerCategoryGuidance:
		return "Guidance"
	case PrayerCategoryThanksgiving:
		return "Thanksgiving"
	default:
		return "General"
	}
}

// prayerStatusLabel returns a human-readable label for a prayer status.
func prayerStatusLabel(status string) string {
	switch status {
	case PrayerStatusActive:
		return "Active"
	case PrayerStatusAnswered:
		return "Answered"
	case PrayerStatusOngoing:
		return "Ongoing"
	case PrayerStatusArchived:
		return "Archived"
	default:
		return "Active"
	}
}

// prayerStatusBadgeClass returns the CSS badge class for a prayer status.
func prayerStatusBadgeClass(status string) string {
	switch status {
	case PrayerStatusActive:
		return "badge-accent"
	case PrayerStatusAnswered:
		return "badge-success"
	case PrayerStatusOngoing:
		return "badge-warning"
	case PrayerStatusArchived:
		return "badge-neutral"
	default:
		return "badge-neutral"
	}
}

// prayerCategoryBadgeClass returns the CSS badge class for a prayer category.
func prayerCategoryBadgeClass(cat string) string {
	switch cat {
	case PrayerCategoryHealing:
		return "badge-warning"
	case PrayerCategoryFamily:
		return "badge-accent"
	case PrayerCategoryWork:
		return "badge-neutral"
	case PrayerCategoryGuidance:
		return "badge-warning"
	case PrayerCategoryThanksgiving:
		return "badge-success"
	default:
		return "badge-neutral"
	}
}

// formatDaysAgo returns "N days ago" or "today" for a timestamp.
func formatDaysAgo(unix int64) string {
	t := time.Unix(unix, 0)
	now := time.Now()
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days <= 0:
		return "Today"
	case days == 1:
		return "Yesterday"
	case days < 7:
		return fmt.Sprintf("%d days ago", days)
	case days < 30:
		return fmt.Sprintf("%d week%s ago", days/7, pluralS(days/7))
	case days < 365:
		return fmt.Sprintf("%d month%s ago", days/30, pluralS(days/30))
	default:
		return fmt.Sprintf("%d year%s ago", days/365, pluralS(days/365))
	}
}

// prayerSubmitterDisplayName returns the name to display for a prayer request.
// If anonymous and the viewer is not a leader, returns "Anonymous".
// Accepts PrayerRequest or PrayerRequestWithUpdates (which embeds PrayerRequest).
func prayerSubmitterDisplayName(pr interface{}, isLeader bool) string {
	var r PrayerRequest
	switch v := pr.(type) {
	case PrayerRequest:
		r = v
	case PrayerRequestWithUpdates:
		r = v.PrayerRequest
	default:
		return "A member"
	}
	if r.IsAnonymous {
		if isLeader {
			if r.SubmittedByName != "" {
				return r.SubmittedByName + " (anonymous to group)"
			}
			if r.SubmittedByEmail != "" {
				return r.SubmittedByEmail + " (anonymous to group)"
			}
			return "Anonymous"
		}
		return "Anonymous"
	}
	if r.SubmittedByName != "" {
		return r.SubmittedByName
	}
	return "A member"
}