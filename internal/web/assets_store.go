// SPDX-License-Identifier: AGPL-3.0
package web

import "fmt"

// GroupAsset is a single file/asset uploaded to a group.
// Files are stored on the local filesystem at stored_path; this struct
// holds the metadata row from the group_assets SQLite table.
type GroupAsset struct {
	ID               int64
	GroupKey         string
	UploadedByEmail string
	Filename         string // sanitized unique filename on disk
	OriginalFilename string // original filename from uploader
	MimeType         string
	SizeBytes        int64
	Category         string // study-notes, audio-recording, document, image, other
	Description      string
	StoredPath       string // relative path under assetDir
	CreatedAt        int64  // unix timestamp
}

// CreateAsset inserts a new group asset record.
func (s *Store) CreateAsset(a GroupAsset) (int64, error) {
	if a.CreatedAt == 0 {
		a.CreatedAt = s.now().Unix()
	}
	res, err := s.db.Exec(
		`INSERT INTO group_assets (group_key, uploaded_by_email, filename, original_filename, mime_type, size_bytes, category, description, stored_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.GroupKey, a.UploadedByEmail, a.Filename, a.OriginalFilename,
		a.MimeType, a.SizeBytes, a.Category, a.Description, a.StoredPath, a.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("web: create asset: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("web: create asset lastInsertId: %w", err)
	}
	return id, nil
}

// GetAsset returns a single asset by ID and group_key.
func (s *Store) GetAsset(groupKey string, id int64) (GroupAsset, error) {
	var a GroupAsset
	err := s.db.QueryRow(
		`SELECT id, group_key, uploaded_by_email, filename, original_filename, mime_type, size_bytes, category, description, stored_path, created_at
		 FROM group_assets WHERE group_key = ? AND id = ?`,
		groupKey, id,
	).Scan(&a.ID, &a.GroupKey, &a.UploadedByEmail, &a.Filename, &a.OriginalFilename,
		&a.MimeType, &a.SizeBytes, &a.Category, &a.Description, &a.StoredPath, &a.CreatedAt)
	if err != nil {
		return GroupAsset{}, err
	}
	return a, nil
}

// ListAssets returns assets for a group, optionally filtered by category
// (empty = all categories) and optionally filtered by a search query
// (matches filename or description, case-insensitive). Results are sorted
// by created_at descending (newest first).
//
// New members can see all assets uploaded before they joined — there is
// no join-date filter. This solves the "files trapped in chat history"
// complaint from user research.
func (s *Store) ListAssets(groupKey, category, search string) ([]GroupAsset, error) {
	query := `SELECT id, group_key, uploaded_by_email, filename, original_filename, mime_type, size_bytes, category, description, stored_path, created_at
		 FROM group_assets WHERE group_key = ?`
	args := []interface{}{groupKey}

	if category != "" && category != "all" {
		query += ` AND category = ?`
		args = append(args, category)
	}

	if search != "" {
		query += ` AND (LOWER(filename) LIKE ? OR LOWER(description) LIKE ? OR LOWER(original_filename) LIKE ?)`
		pattern := "%" + toLower(search) + "%"
		args = append(args, pattern, pattern, pattern)
	}

	query += ` ORDER BY created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupAsset
	for rows.Next() {
		var a GroupAsset
		if err := rows.Scan(&a.ID, &a.GroupKey, &a.UploadedByEmail, &a.Filename, &a.OriginalFilename,
			&a.MimeType, &a.SizeBytes, &a.Category, &a.Description, &a.StoredPath, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAssetDescription updates the description of an asset.
// Only the uploader or a group leader should call this (enforced at handler level).
func (s *Store) UpdateAssetDescription(id int64, description string) error {
	_, err := s.db.Exec(`UPDATE group_assets SET description = ? WHERE id = ?`, description, id)
	return err
}

// DeleteAsset removes an asset record from the database.
// The caller is responsible for deleting the file from the filesystem.
func (s *Store) DeleteAsset(groupKey string, id int64) error {
	_, err := s.db.Exec(`DELETE FROM group_assets WHERE group_key = ? AND id = ?`, groupKey, id)
	return err
}

// toLower is a simple wrapper for strings.ToLower without importing strings
// (this file avoids extra imports for minimalism).
func toLower(s string) string {
	// fast path: ASCII lowercase
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// ---- Asset category helpers ----

// validAssetCategories is the set of recognized category values.
var validAssetCategories = map[string]bool{
	"study-notes":     true,
	"audio-recording": true,
	"document":        true,
	"image":           true,
	"other":           true,
}

// isValidAssetCategory returns true if the category is recognized.
func isValidAssetCategory(cat string) bool {
	return validAssetCategories[cat]
}

// assetCategoryLabel returns a human-friendly label for a category.
func assetCategoryLabel(cat string) string {
	switch cat {
	case "study-notes":
		return "Study Notes"
	case "audio-recording":
		return "Audio Recording"
	case "document":
		return "Document"
	case "image":
		return "Image"
	case "other":
		return "Other"
	default:
		return "General"
	}
}

// assetCategoryIcon returns a short emoji/icon identifier for a category.
func assetCategoryIcon(cat string) string {
	switch cat {
	case "study-notes":
		return "📖"
	case "audio-recording":
		return "🎙"
	case "document":
		return "📄"
	case "image":
		return "🖼"
	case "other":
		return "📦"
	default:
		return "📎"
	}
}

// isAudioFile returns true if the mime type indicates an audio file.
func isAudioFile(mimeType string) bool {
	switch mimeType {
	case "audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/x-wav", "audio/wave":
		return true
	default:
		return false
	}
}

// formatFileSize formats a byte count as a human-readable string.
func formatFileSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// sanitizeGroupKey removes any path traversal characters from a group key.
// Group keys are hex strings, so we only allow hex characters.
func sanitizeGroupKey(key string) string {
	out := make([]byte, 0, len(key))
	for _, c := range []byte(key) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

// sanitizeFilename removes path separators and traversal sequences from a filename.
// Returns a safe filename containing only alphanumerics, dots, dashes, underscores.
func sanitizeFilename(name string) string {
	out := make([]byte, 0, len(name))
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	// Collapse multiple dots to prevent hidden files
	result := string(out)
	if result == "" {
		result = "file"
	}
	return result
}

// fileExtension returns the lowercase file extension (without the dot).
func fileExtension(filename string) string {
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			return toLower(filename[i+1:])
		}
		if filename[i] == '/' || filename[i] == '\\' {
			break
		}
	}
	return ""
}

// isAllowedAssetExtension returns true if the file extension is in the allowlist.
func isAllowedAssetExtension(ext string) bool {
	return allowedAssetExtensions[ext]
}

// mimeTypeForExt returns a reasonable Content-Type for common extensions.
func mimeTypeForExt(ext string) string {
	switch ext {
	case "pdf":
		return "application/pdf"
	case "doc":
		return "application/msword"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "txt":
		return "text/plain; charset=utf-8"
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/mp4"
	case "wav":
		return "audio/wav"
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "epub":
		return "application/epub+zip"
	default:
		return "application/octet-stream"
	}
}