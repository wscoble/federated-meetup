// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"database/sql"
	"fmt"
	"time"
)

// ---- Schema migrations for member management ----

// migrateMembers creates member-related tables and adds columns to
// existing tables. Called by Store.migrate() after base schema.
func (s *Store) migrateMembers() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS group_members (
			group_key          TEXT NOT NULL DEFAULT '',
			user_email         TEXT NOT NULL DEFAULT '',
			display_name       TEXT NOT NULL DEFAULT '',
			role               TEXT NOT NULL DEFAULT 'member',
			phone_optional     TEXT NOT NULL DEFAULT '',
			show_phone_to_group INTEGER NOT NULL DEFAULT 0,
			joined_at          INTEGER NOT NULL DEFAULT 0,
			onboarded_at       INTEGER NOT NULL DEFAULT 0,
			created_at         INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_key, user_email)
		)`,
		`CREATE TABLE IF NOT EXISTS group_settings (
			group_key    TEXT PRIMARY KEY,
			guidelines    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS member_invites (
			token         TEXT PRIMARY KEY,
			group_key     TEXT NOT NULL DEFAULT '',
			invitee_email TEXT NOT NULL DEFAULT '',
			invited_by    TEXT NOT NULL DEFAULT '',
			accepted      INTEGER NOT NULL DEFAULT 0,
			created_at    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS transfer_logs (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			group_key     TEXT NOT NULL DEFAULT '',
			from_email    TEXT NOT NULL DEFAULT '',
			to_email      TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_members_group ON group_members(group_key)`,
		`CREATE INDEX IF NOT EXISTS idx_members_email ON group_members(user_email)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_group ON member_invites(group_key)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// ---- Member types ----

// MemberRole is the role of a member within a group.
type MemberRole string

const (
	RoleLeader    MemberRole = "leader"
	RoleCoLeader  MemberRole = "co-leader"
	RoleMember    MemberRole = "member"
)

// GroupMember represents a single member of a group.
type GroupMember struct {
	GroupKey         string
	UserEmail        string
	DisplayName      string
	Role             MemberRole
	PhoneOptional    string
	ShowPhoneToGroup bool
	JoinedAt         int64
	OnboardedAt      int64 // 0 = not yet onboarded
	CreatedAt        int64
}

// GroupSettings holds leader-configured group settings (e.g. guidelines).
type GroupSettings struct {
	GroupKey  string
	Guidelines string
}

// MemberInvite represents an invitation to join a group.
type MemberInvite struct {
	Token       string
	GroupKey    string
	InviteeEmail string
	InvitedBy   string
	Accepted    bool
	CreatedAt   int64
}

// TransferLog records a leadership transfer event.
type TransferLog struct {
	ID         int64
	GroupKey   string
	FromEmail  string
	ToEmail    string
	Status     string // "initiated", "accepted", "declined", "cancelled"
	CreatedAt  int64
}

// ---- Member CRUD ----

// AddMember inserts a new group member. If the member already exists,
// updates their role and display name instead (upsert).
func (s *Store) AddMember(m GroupMember) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = s.now().Unix()
	}
	if m.JoinedAt == 0 {
		m.JoinedAt = s.now().Unix()
	}
	showPhone := 0
	if m.ShowPhoneToGroup {
		showPhone = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO group_members (group_key, user_email, display_name, role, phone_optional, show_phone_to_group, joined_at, onboarded_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(group_key, user_email) DO UPDATE SET
		   display_name=excluded.display_name,
		   role=excluded.role,
		   phone_optional=excluded.phone_optional,
		   show_phone_to_group=excluded.show_phone_to_group`,
		m.GroupKey, m.UserEmail, m.DisplayName, string(m.Role), m.PhoneOptional, showPhone, m.JoinedAt, m.OnboardedAt, m.CreatedAt,
	)
	return err
}

// RemoveMember removes a member from a group.
func (s *Store) RemoveMember(groupKey, userEmail string) error {
	_, err := s.db.Exec(`DELETE FROM group_members WHERE group_key = ? AND user_email = ?`, groupKey, userEmail)
	return err
}

// GetMember returns a single member by group_key + email.
func (s *Store) GetMember(groupKey, userEmail string) (GroupMember, error) {
	var m GroupMember
	var role string
	var showPhone int
	err := s.db.QueryRow(
		`SELECT group_key, user_email, display_name, role, phone_optional, show_phone_to_group, joined_at, onboarded_at, created_at
		 FROM group_members WHERE group_key = ? AND user_email = ?`,
		groupKey, userEmail,
	).Scan(&m.GroupKey, &m.UserEmail, &m.DisplayName, &role, &m.PhoneOptional, &showPhone, &m.JoinedAt, &m.OnboardedAt, &m.CreatedAt)
	if err != nil {
		return GroupMember{}, err
	}
	m.Role = MemberRole(role)
	m.ShowPhoneToGroup = showPhone != 0
	return m, nil
}

// ListMembers returns all members of a group, sorted by role priority then name.
func (s *Store) ListMembers(groupKey string) ([]GroupMember, error) {
	rows, err := s.db.Query(
		`SELECT group_key, user_email, display_name, role, phone_optional, show_phone_to_group, joined_at, onboarded_at, created_at
		 FROM group_members WHERE group_key = ?
		 ORDER BY CASE role
		   WHEN 'leader' THEN 0
		   WHEN 'co-leader' THEN 1
		   WHEN 'member' THEN 2
		   ELSE 3
		 END, display_name ASC`,
		groupKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMember
	for rows.Next() {
		var m GroupMember
		var role string
		var showPhone int
		if err := rows.Scan(&m.GroupKey, &m.UserEmail, &m.DisplayName, &role, &m.PhoneOptional, &showPhone, &m.JoinedAt, &m.OnboardedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = MemberRole(role)
		m.ShowPhoneToGroup = showPhone != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMemberRole changes a member's role within a group.
func (s *Store) UpdateMemberRole(groupKey, userEmail string, role MemberRole) error {
	_, err := s.db.Exec(
		`UPDATE group_members SET role = ? WHERE group_key = ? AND user_email = ?`,
		string(role), groupKey, userEmail,
	)
	return err
}

// UpdateMemberProfile updates a member's display name, phone, and privacy setting.
func (s *Store) UpdateMemberProfile(groupKey, userEmail, displayName, phone string, showPhone bool) error {
	sp := 0
	if showPhone {
		sp = 1
	}
	_, err := s.db.Exec(
		`UPDATE group_members SET display_name = ?, phone_optional = ?, show_phone_to_group = ?
		 WHERE group_key = ? AND user_email = ?`,
		displayName, phone, sp, groupKey, userEmail,
	)
	return err
}

// MarkOnboarded sets the onboarded_at timestamp for a member.
func (s *Store) MarkOnboarded(groupKey, userEmail string) error {
	_, err := s.db.Exec(
		`UPDATE group_members SET onboarded_at = ? WHERE group_key = ? AND user_email = ?`,
		s.now().Unix(), groupKey, userEmail,
	)
	return err
}

// ---- Group settings (guidelines) ----

// GetGroupSettings returns settings for a group (guidelines, etc.).
func (s *Store) GetGroupSettings(groupKey string) (GroupSettings, error) {
	var gs GroupSettings
	gs.GroupKey = groupKey
	err := s.db.QueryRow(
		`SELECT group_key, guidelines FROM group_settings WHERE group_key = ?`,
		groupKey,
	).Scan(&gs.GroupKey, &gs.Guidelines)
	if err == sql.ErrNoRows {
		return gs, nil // no settings yet — return empty
	}
	return gs, err
}

// SetGroupGuidelines sets the group guidelines text (leader action).
func (s *Store) SetGroupGuidelines(groupKey, guidelines string) error {
	_, err := s.db.Exec(
		`INSERT INTO group_settings (group_key, guidelines) VALUES (?, ?)
		 ON CONFLICT(group_key) DO UPDATE SET guidelines=excluded.guidelines`,
		groupKey, guidelines,
	)
	return err
}

// ---- Invitations ----

// CreateInvite creates a member invitation with a token.
func (s *Store) CreateInvite(invite MemberInvite) error {
	if invite.CreatedAt == 0 {
		invite.CreatedAt = s.now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO member_invites (token, group_key, invitee_email, invited_by, accepted, created_at)
		 VALUES (?, ?, ?, ?, 0, ?)`,
		invite.Token, invite.GroupKey, invite.InviteeEmail, invite.InvitedBy, invite.CreatedAt,
	)
	return err
}

// GetInvite returns an invitation by token.
func (s *Store) GetInvite(token string) (MemberInvite, error) {
	var inv MemberInvite
	var accepted int
	err := s.db.QueryRow(
		`SELECT token, group_key, invitee_email, invited_by, accepted, created_at
		 FROM member_invites WHERE token = ?`,
		token,
	).Scan(&inv.Token, &inv.GroupKey, &inv.InviteeEmail, &inv.InvitedBy, &accepted, &inv.CreatedAt)
	if err != nil {
		return MemberInvite{}, err
	}
	inv.Accepted = accepted != 0
	return inv, nil
}

// AcceptInvite marks an invitation as accepted.
func (s *Store) AcceptInvite(token string) error {
	_, err := s.db.Exec(`UPDATE member_invites SET accepted = 1 WHERE token = ?`, token)
	return err
}

// ---- Leadership transfer ----

// LogTransfer logs a leadership transfer event.
func (s *Store) LogTransfer(t TransferLog) error {
	if t.CreatedAt == 0 {
		t.CreatedAt = s.now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO transfer_logs (group_key, from_email, to_email, status, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		t.GroupKey, t.FromEmail, t.ToEmail, t.Status, t.CreatedAt,
	)
	return err
}

// ListTransfers returns transfer logs for a group.
func (s *Store) ListTransfers(groupKey string) ([]TransferLog, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, from_email, to_email, status, created_at
		 FROM transfer_logs WHERE group_key = ? ORDER BY created_at DESC`,
		groupKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TransferLog
	for rows.Next() {
		var t TransferLog
		if err := rows.Scan(&t.ID, &t.GroupKey, &t.FromEmail, &t.ToEmail, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TransferOwnership transfers the leader role from one member to another.
// In a single transaction: demote old leader to member, promote new leader,
// and update the organizer session to the new leader.
func (s *Store) TransferOwnership(groupKey, fromEmail, toEmail, newSessionToken string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Demote old leader to co-leader
	if _, err := tx.Exec(
		`UPDATE group_members SET role = 'co-leader' WHERE group_key = ? AND user_email = ?`,
		groupKey, fromEmail,
	); err != nil {
		return err
	}

	// Promote new leader
	if _, err := tx.Exec(
		`UPDATE group_members SET role = 'leader' WHERE group_key = ? AND user_email = ?`,
		groupKey, toEmail,
	); err != nil {
		return err
	}

	// Update organizer session: delete old sessions for this group and create
	// a new one tied to the new leader's token.
	if _, err := tx.Exec(
		`DELETE FROM organizer_sessions WHERE group_key = ?`,
		groupKey,
	); err != nil {
		return err
	}
	expiresAt := s.now().Add(7 * 24 * time.Hour).Unix()
	if _, err := tx.Exec(
		`INSERT INTO organizer_sessions (token, group_key, expires_at) VALUES (?, ?, ?)`,
		newSessionToken, groupKey, expiresAt,
	); err != nil {
		return err
	}

	// Log the transfer
	if _, err := tx.Exec(
		`INSERT INTO transfer_logs (group_key, from_email, to_email, status, created_at)
		 VALUES (?, ?, ?, 'accepted', ?)`,
		groupKey, fromEmail, toEmail, s.now().Unix(),
	); err != nil {
		return err
	}

	return tx.Commit()
}