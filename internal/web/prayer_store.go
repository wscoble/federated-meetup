// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"database/sql"
	"fmt"
	"time"
)

// ---- Prayer Requests ----

// PrayerRequest is a single prayer request submitted to a group.
type PrayerRequest struct {
	ID               int64
	GroupKey         string
	SubmittedByEmail string
	SubmittedByName  string
	Title            string
	Body             string
	Category         string
	Status           string
	IsAnonymous      bool
	CreatedAt        int64
	UpdatedAt        int64
}

// PrayerUpdate is an update/timeline entry on a prayer request.
type PrayerUpdate struct {
	ID              int64
	PrayerRequestID int64
	UpdateText      string
	StatusChange    string
	CreatedBy       string
	CreatedAt       int64
}

// PrayerCategory values.
const (
	PrayerCategoryGeneral      = "general"
	PrayerCategoryHealing      = "healing"
	PrayerCategoryFamily       = "family"
	PrayerCategoryWork         = "work"
	PrayerCategoryGuidance     = "guidance"
	PrayerCategoryThanksgiving = "thanksgiving"
)

// validPrayerCategories is the set of allowed category values.
var validPrayerCategories = map[string]bool{
	PrayerCategoryGeneral:      true,
	PrayerCategoryHealing:      true,
	PrayerCategoryFamily:       true,
	PrayerCategoryWork:         true,
	PrayerCategoryGuidance:     true,
	PrayerCategoryThanksgiving: true,
}

// PrayerStatus values.
const (
	PrayerStatusActive   = "active"
	PrayerStatusAnswered  = "answered"
	PrayerStatusOngoing   = "ongoing"
	PrayerStatusArchived  = "archived"
)

// validPrayerStatuses is the set of allowed status values.
var validPrayerStatuses = map[string]bool{
	PrayerStatusActive:  true,
	PrayerStatusAnswered: true,
	PrayerStatusOngoing:  true,
	PrayerStatusArchived: true,
}

// validatePrayerCategory returns true if cat is a valid category.
func validatePrayerCategory(cat string) bool {
	return validPrayerCategories[cat]
}

// validatePrayerStatus returns true if st is a valid status.
func validatePrayerStatus(st string) bool {
	return validPrayerStatuses[st]
}

// CreatePrayerRequest inserts a new prayer request. Returns the created row ID.
func (s *Store) CreatePrayerRequest(pr PrayerRequest) (int64, error) {
	now := s.now().Unix()
	if pr.CreatedAt == 0 {
		pr.CreatedAt = now
	}
	pr.UpdatedAt = now

	anon := 0
	if pr.IsAnonymous {
		anon = 1
	}
	if pr.Category == "" {
		pr.Category = PrayerCategoryGeneral
	}
	if pr.Status == "" {
		pr.Status = PrayerStatusActive
	}

	res, err := s.db.Exec(
		`INSERT INTO prayer_requests (group_key, submitted_by_email, submitted_by_name, title, body, category, status, is_anonymous, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.GroupKey, pr.SubmittedByEmail, pr.SubmittedByName, pr.Title, pr.Body, pr.Category, pr.Status, anon, pr.CreatedAt, pr.UpdatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("create prayer request: %w", err)
	}
	return res.LastInsertId()
}

// GetPrayerRequest returns a prayer request by ID.
func (s *Store) GetPrayerRequest(id int64) (PrayerRequest, error) {
	var pr PrayerRequest
	var anon int
	err := s.db.QueryRow(
		`SELECT id, group_key, submitted_by_email, submitted_by_name, title, body, category, status, is_anonymous, created_at, updated_at
		 FROM prayer_requests WHERE id = ?`,
		id,
	).Scan(&pr.ID, &pr.GroupKey, &pr.SubmittedByEmail, &pr.SubmittedByName, &pr.Title, &pr.Body, &pr.Category, &pr.Status, &anon, &pr.CreatedAt, &pr.UpdatedAt)
	if err != nil {
		return PrayerRequest{}, err
	}
	pr.IsAnonymous = anon != 0
	return pr, nil
}

// ListPrayerRequests returns all prayer requests for a group, sorted newest-first.
func (s *Store) ListPrayerRequests(groupKey string) ([]PrayerRequest, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, submitted_by_email, submitted_by_name, title, body, category, status, is_anonymous, created_at, updated_at
		 FROM prayer_requests WHERE group_key = ? ORDER BY created_at DESC`,
		groupKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrayerRequest
	for rows.Next() {
		var pr PrayerRequest
		var anon int
		if err := rows.Scan(&pr.ID, &pr.GroupKey, &pr.SubmittedByEmail, &pr.SubmittedByName, &pr.Title, &pr.Body, &pr.Category, &pr.Status, &anon, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
			return nil, err
		}
		pr.IsAnonymous = anon != 0
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ListPrayerRequestsByStatus returns prayer requests for a group filtered by status.
func (s *Store) ListPrayerRequestsByStatus(groupKey, status string) ([]PrayerRequest, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, submitted_by_email, submitted_by_name, title, body, category, status, is_anonymous, created_at, updated_at
		 FROM prayer_requests WHERE group_key = ? AND status = ? ORDER BY created_at DESC`,
		groupKey, status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrayerRequest
	for rows.Next() {
		var pr PrayerRequest
		var anon int
		if err := rows.Scan(&pr.ID, &pr.GroupKey, &pr.SubmittedByEmail, &pr.SubmittedByName, &pr.Title, &pr.Body, &pr.Category, &pr.Status, &anon, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
			return nil, err
		}
		pr.IsAnonymous = anon != 0
		out = append(out, pr)
	}
	return out, rows.Err()
}

// UpdatePrayerRequestStatus updates the status of a prayer request and bumps updated_at.
func (s *Store) UpdatePrayerRequestStatus(id int64, status string) error {
	if !validatePrayerStatus(status) {
		return fmt.Errorf("invalid prayer status: %s", status)
	}
	_, err := s.db.Exec(
		`UPDATE prayer_requests SET status = ?, updated_at = ? WHERE id = ?`,
		status, s.now().Unix(), id,
	)
	return err
}

// CreatePrayerUpdate inserts a timeline update on a prayer request.
// If statusChange is non-empty, also updates the parent request's status.
func (s *Store) CreatePrayerUpdate(pu PrayerUpdate) (int64, error) {
	if pu.CreatedAt == 0 {
		pu.CreatedAt = s.now().Unix()
	}
	res, err := s.db.Exec(
		`INSERT INTO prayer_updates (prayer_request_id, update_text, status_change, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		pu.PrayerRequestID, pu.UpdateText, pu.StatusChange, pu.CreatedBy, pu.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("create prayer update: %w", err)
	}
	updateID, _ := res.LastInsertId()

	// If a status change was specified, update the parent request.
	if pu.StatusChange != "" && validatePrayerStatus(pu.StatusChange) {
		_ = s.UpdatePrayerRequestStatus(pu.PrayerRequestID, pu.StatusChange)
	}

	return updateID, nil
}

// ListPrayerUpdates returns all updates for a prayer request, chronological order.
func (s *Store) ListPrayerUpdates(prayerRequestID int64) ([]PrayerUpdate, error) {
	rows, err := s.db.Query(
		`SELECT id, prayer_request_id, update_text, status_change, created_by, created_at
		 FROM prayer_updates WHERE prayer_request_id = ? ORDER BY created_at ASC`,
		prayerRequestID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrayerUpdate
	for rows.Next() {
		var pu PrayerUpdate
		if err := rows.Scan(&pu.ID, &pu.PrayerRequestID, &pu.UpdateText, &pu.StatusChange, &pu.CreatedBy, &pu.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pu)
	}
	return out, rows.Err()
}

// StalePrayerRequests returns prayer requests for a group that haven't been
// updated in `days` or more days and are not archived or answered.
func (s *Store) StalePrayerRequests(groupKey string, days int) ([]PrayerRequest, error) {
	cutoff := s.now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.db.Query(
		`SELECT id, group_key, submitted_by_email, submitted_by_name, title, body, category, status, is_anonymous, created_at, updated_at
		 FROM prayer_requests
		 WHERE group_key = ? AND updated_at < ? AND status NOT IN (?, ?)
		 ORDER BY updated_at ASC`,
		groupKey, cutoff, PrayerStatusArchived, PrayerStatusAnswered,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrayerRequest
	for rows.Next() {
		var pr PrayerRequest
		var anon int
		if err := rows.Scan(&pr.ID, &pr.GroupKey, &pr.SubmittedByEmail, &pr.SubmittedByName, &pr.Title, &pr.Body, &pr.Category, &pr.Status, &anon, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
			return nil, err
		}
		pr.IsAnonymous = anon != 0
		out = append(out, pr)
	}
	return out, rows.Err()
}

// PrayerRequestWithUpdates is a convenience struct for display: the request
// plus its timeline of updates.
type PrayerRequestWithUpdates struct {
	PrayerRequest
	Updates []PrayerUpdate
}

// GetPrayerRequestsWithUpdates returns all prayer requests for a group,
// each with their update timeline loaded.
func (s *Store) GetPrayerRequestsWithUpdates(groupKey string) ([]PrayerRequestWithUpdates, error) {
	requests, err := s.ListPrayerRequests(groupKey)
	if err != nil {
		return nil, err
	}
	var out []PrayerRequestWithUpdates
	for _, pr := range requests {
		updates, err := s.ListPrayerUpdates(pr.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, PrayerRequestWithUpdates{
			PrayerRequest: pr,
			Updates:       updates,
		})
	}
	return out, nil
}

// CountPrayerRequests returns the total count of prayer requests for a group.
func (s *Store) CountPrayerRequests(groupKey string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM prayer_requests WHERE group_key = ?`,
		groupKey,
	).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}