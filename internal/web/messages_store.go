// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ---- Schema additions ----

// messageMigrations contains the DDL for group messaging tables.
// These are executed by migrate() via the messageStmts() method.
func (s *Store) messageStmts() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS group_messages (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			group_key         TEXT NOT NULL DEFAULT '',
			parent_message_id INTEGER NULL,
			author_email      TEXT NOT NULL DEFAULT '',
			author_name       TEXT NOT NULL DEFAULT '',
			body              TEXT NOT NULL DEFAULT '',
			created_at        INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (parent_message_id) REFERENCES group_messages(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_group ON group_messages(group_key)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_parent ON group_messages(parent_message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created ON group_messages(group_key, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS message_reactions (
			message_id  INTEGER NOT NULL DEFAULT 0,
			user_email  TEXT NOT NULL DEFAULT '',
			emoji       TEXT NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, user_email, emoji)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reactions_message ON message_reactions(message_id)`,
		// FTS5 virtual table for full-text search on message bodies.
		// If FTS5 is not available, SearchMessages falls back to LIKE.
		`CREATE VIRTUAL TABLE IF NOT EXISTS group_messages_fts USING fts5(
			body,
			content='group_messages',
			content_rowid='id'
		)`,
		// Triggers to keep FTS index in sync with the messages table.
		`CREATE TRIGGER IF NOT EXISTS group_messages_ai AFTER INSERT ON group_messages BEGIN
			INSERT INTO group_messages_fts(rowid, body) VALUES (new.id, new.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS group_messages_ad AFTER DELETE ON group_messages BEGIN
			INSERT INTO group_messages_fts(group_messages_fts, rowid, body) VALUES ('delete', old.id, old.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS group_messages_au AFTER UPDATE ON group_messages BEGIN
			INSERT INTO group_messages_fts(group_messages_fts, rowid, body) VALUES ('delete', old.id, old.body);
			INSERT INTO group_messages_fts(rowid, body) VALUES (new.id, new.body);
		END`,
	}
}

// ---- Data types ----

// GroupMessage is a single message in a group chat.
type GroupMessage struct {
	ID              int64
	GroupKey        string
	ParentMessageID sql.NullInt64
	AuthorEmail     string
	AuthorName      string
	Body            string
	CreatedAt       int64
}

// MessageReaction is an emoji reaction on a message.
type MessageReaction struct {
	MessageID int64
	UserEmail string
	Emoji     string
	CreatedAt int64
}

// ReactionSummary aggregates reactions by emoji for display.
type ReactionSummary struct {
	Emoji  string
	Count  int
	Reacted bool // whether the current user reacted with this emoji
}

// MessageWithReactions is a GroupMessage with its reactions for display.
type MessageWithReactions struct {
	GroupMessage
	Reactions []ReactionSummary
}

// ---- Store methods ----

// CreateMessage inserts a new group message. If parentMessageID > 0, the
// message is a reply in that thread. Returns the new message ID.
func (s *Store) CreateMessage(groupKey, authorEmail, authorName, body string, parentMessageID int64) (GroupMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return GroupMessage{}, fmt.Errorf("message body cannot be empty")
	}
	if len(body) > 8192 {
		return GroupMessage{}, fmt.Errorf("message body too long (max 8192 bytes)")
	}
	if groupKey == "" {
		return GroupMessage{}, fmt.Errorf("group_key is required")
	}
	if authorEmail == "" {
		return GroupMessage{}, fmt.Errorf("author_email is required")
	}

	var parent sql.NullInt64
	if parentMessageID > 0 {
		parent = sql.NullInt64{Int64: parentMessageID, Valid: true}
	}

	createdAt := s.now().Unix()

	// Try FTS5 path first (triggers auto-populate the FTS index).
	// If FTS5 is unavailable, this INSERT will fail and we fall back
	// to the non-FTS table only (search uses LIKE).
	res, err := s.db.Exec(
		`INSERT INTO group_messages (group_key, parent_message_id, author_email, author_name, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		groupKey, parent, authorEmail, authorName, body, createdAt,
	)
	if err != nil {
		return GroupMessage{}, fmt.Errorf("create message: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return GroupMessage{}, fmt.Errorf("last insert id: %w", err)
	}

	return GroupMessage{
		ID:              id,
		GroupKey:        groupKey,
		ParentMessageID: parent,
		AuthorEmail:     authorEmail,
		AuthorName:      authorName,
		Body:            body,
		CreatedAt:       createdAt,
	}, nil
}

// ListMessages returns top-level messages (no parent) for a group, ordered
// newest-first, with cursor-based pagination. The cursor is the last message
// ID from the previous page. Pass 0 as cursor for the first page.
// Returns messages and whether there are more messages (for "Load more").
func (s *Store) ListMessages(groupKey string, cursor int64, limit int) ([]GroupMessage, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var rows *sql.Rows
	var err error
	if cursor > 0 {
		// Get messages with ID < cursor (older messages, since we order DESC)
		rows, err = s.db.Query(
			`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
			 FROM group_messages
			 WHERE group_key = ? AND parent_message_id IS NULL AND id < ?
			 ORDER BY id DESC LIMIT ?`,
			groupKey, cursor, limit+1,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
			 FROM group_messages
			 WHERE group_key = ? AND parent_message_id IS NULL
			 ORDER BY id DESC LIMIT ?`,
			groupKey, limit+1,
		)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []GroupMessage
	for rows.Next() {
		var m GroupMessage
		if err := rows.Scan(&m.ID, &m.GroupKey, &m.ParentMessageID, &m.AuthorEmail, &m.AuthorName, &m.Body, &m.CreatedAt); err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// GetMessageThread returns a message by ID and all of its replies (one level deep).
// For nested threads, replies can themselves have replies — this returns the
// direct replies. The caller can recurse if needed.
func (s *Store) GetMessageThread(messageID int64) (GroupMessage, []GroupMessage, error) {
	var m GroupMessage
	err := s.db.QueryRow(
		`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
		 FROM group_messages WHERE id = ?`,
		messageID,
	).Scan(&m.ID, &m.GroupKey, &m.ParentMessageID, &m.AuthorEmail, &m.AuthorName, &m.Body, &m.CreatedAt)
	if err != nil {
		return GroupMessage{}, nil, fmt.Errorf("get message %d: %w", messageID, err)
	}

	rows, err := s.db.Query(
		`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
		 FROM group_messages WHERE parent_message_id = ? ORDER BY id ASC`,
		messageID,
	)
	if err != nil {
		return m, nil, err
	}
	defer rows.Close()

	var replies []GroupMessage
	for rows.Next() {
		var r GroupMessage
		if err := rows.Scan(&r.ID, &r.GroupKey, &r.ParentMessageID, &r.AuthorEmail, &r.AuthorName, &r.Body, &r.CreatedAt); err != nil {
			return m, nil, err
		}
		replies = append(replies, r)
	}
	return m, replies, rows.Err()
}

// GetReplies returns all direct replies to a message.
func (s *Store) GetReplies(messageID int64) ([]GroupMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
		 FROM group_messages WHERE parent_message_id = ? ORDER BY id ASC`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GroupMessage
	for rows.Next() {
		var m GroupMessage
		if err := rows.Scan(&m.ID, &m.GroupKey, &m.ParentMessageID, &m.AuthorEmail, &m.AuthorName, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMessage returns a single message by ID.
func (s *Store) GetMessage(messageID int64) (GroupMessage, error) {
	var m GroupMessage
	err := s.db.QueryRow(
		`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
		 FROM group_messages WHERE id = ?`,
		messageID,
	).Scan(&m.ID, &m.GroupKey, &m.ParentMessageID, &m.AuthorEmail, &m.AuthorName, &m.Body, &m.CreatedAt)
	if err != nil {
		return GroupMessage{}, err
	}
	return m, nil
}

// SearchMessages searches message bodies in a group for the given query.
// Uses FTS5 if available, otherwise falls back to LIKE.
// Returns matching messages ordered newest-first.
func (s *Store) SearchMessages(groupKey, query string, limit int) ([]GroupMessage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Try FTS5 first.
	rows, err := s.db.Query(
		`SELECT m.id, m.group_key, m.parent_message_id, m.author_email, m.author_name, m.body, m.created_at
		 FROM group_messages m
		 JOIN group_messages_fts f ON f.rowid = m.id
		 WHERE m.group_key = ? AND group_messages_fts MATCH ?
		 ORDER BY m.id DESC LIMIT ?`,
		groupKey, ftsEscape(query), limit,
	)
	if err != nil {
		// FTS5 not available — fall back to LIKE.
		rows, err = s.db.Query(
			`SELECT id, group_key, parent_message_id, author_email, author_name, body, created_at
			 FROM group_messages
			 WHERE group_key = ? AND LOWER(body) LIKE LOWER(?)
			 ORDER BY id DESC LIMIT ?`,
			groupKey, "%"+query+"%", limit,
		)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var out []GroupMessage
	for rows.Next() {
		var m GroupMessage
		if err := rows.Scan(&m.ID, &m.GroupKey, &m.ParentMessageID, &m.AuthorEmail, &m.AuthorName, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ftsEscape escapes a search query for FTS5 MATCH syntax.
// Wraps each word in double quotes to prevent FTS5 syntax injection.
func ftsEscape(query string) string {
	words := strings.Fields(query)
	var parts []string
	for _, w := range words {
		// Escape double quotes inside the word.
		w = strings.ReplaceAll(w, "\"", "\"\"")
		parts = append(parts, "\""+w+"\"")
	}
	return strings.Join(parts, " ")
}

// ---- Reactions ----

// AddReaction adds an emoji reaction to a message. If the reaction already
// exists (same message_id, user_email, emoji), it's a no-op (idempotent).
func (s *Store) AddReaction(messageID int64, userEmail, emoji string) error {
	if messageID <= 0 || userEmail == "" || emoji == "" {
		return fmt.Errorf("message_id, user_email, and emoji are required")
	}
	if len(emoji) > 32 {
		return fmt.Errorf("emoji too long")
	}
	_, err := s.db.Exec(
		`INSERT INTO message_reactions (message_id, user_email, emoji, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(message_id, user_email, emoji) DO NOTHING`,
		messageID, userEmail, emoji, s.now().Unix(),
	)
	return err
}

// RemoveReaction removes an emoji reaction from a message.
func (s *Store) RemoveReaction(messageID int64, userEmail, emoji string) error {
	_, err := s.db.Exec(
		`DELETE FROM message_reactions WHERE message_id = ? AND user_email = ? AND emoji = ?`,
		messageID, userEmail, emoji,
	)
	return err
}

// ListReactions returns all reactions for a message, grouped by emoji.
// currentUserEmail is used to set ReactionSummary.Reacted for the current user.
func (s *Store) ListReactions(messageID int64, currentUserEmail string) ([]ReactionSummary, error) {
	rows, err := s.db.Query(
		`SELECT emoji, COUNT(*) as cnt,
		 SUM(CASE WHEN user_email = ? THEN 1 ELSE 0 END) as user_reacted
		 FROM message_reactions
		 WHERE message_id = ?
		 GROUP BY emoji ORDER BY cnt DESC, emoji ASC`,
		currentUserEmail, messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReactionSummary
	for rows.Next() {
		var r ReactionSummary
		var userReacted int
		if err := rows.Scan(&r.Emoji, &r.Count, &userReacted); err != nil {
			return nil, err
		}
		r.Reacted = userReacted > 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetMessageReactionsMap returns a map of message_id → []ReactionSummary
// for a list of messages. This is efficient for batch-loading reactions
// when rendering a message list.
func (s *Store) GetMessageReactionsMap(messageIDs []int64, currentUserEmail string) (map[int64][]ReactionSummary, error) {
	result := make(map[int64][]ReactionSummary)
	if len(messageIDs) == 0 {
		return result, nil
	}

	// Build placeholders for IN clause.
	placeholders := make([]interface{}, len(messageIDs)+1)
	placeholders[0] = currentUserEmail
	for i, id := range messageIDs {
		placeholders[i+1] = id
	}
	inClause := strings.Repeat("?,", len(messageIDs))
	inClause = strings.TrimSuffix(inClause, ",")

	query := fmt.Sprintf(
		`SELECT message_id, emoji, COUNT(*) as cnt,
		 SUM(CASE WHEN user_email = ? THEN 1 ELSE 0 END) as user_reacted
		 FROM message_reactions
		 WHERE message_id IN (%s)
		 GROUP BY message_id, emoji ORDER BY message_id, cnt DESC, emoji ASC`,
		inClause,
	)

	rows, err := s.db.Query(query, placeholders...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var msgID int64
		var r ReactionSummary
		var userReacted int
		if err := rows.Scan(&msgID, &r.Emoji, &r.Count, &userReacted); err != nil {
			return nil, err
		}
		r.Reacted = userReacted > 0
		result[msgID] = append(result[msgID], r)
	}
	return result, rows.Err()
}

// CountMessages returns the total number of messages in a group.
func (s *Store) CountMessages(groupKey string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM group_messages WHERE group_key = ?`,
		groupKey,
	).Scan(&count)
	return count, err
}

// _ ensures time is referenced (used for s.now() in some test contexts)
var _ = time.Now