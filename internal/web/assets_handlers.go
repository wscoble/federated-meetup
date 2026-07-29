// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// assetsData is the template data for the assets page.
type assetsData struct {
	pageBase
	GroupKey        string
	GroupName       string
	CanonicalName   string
	Assets          []GroupAsset
	AssetCategory   string
	AssetSearch     string
	IsAuthed        bool
	UserEmail       string
	UserName        string
}

// handleGroupAssets renders the group assets page (file sharing).
func (s *Server) handleGroupAssets(w http.ResponseWriter, r *http.Request) {
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

	// Parse category and search filters
	category := r.URL.Query().Get("category")
	search := r.URL.Query().Get("q")

	assets, _ := s.store.ListAssets(group.GroupKey, category, search)

	// Check if the user is authenticated (organizer)
	_, isAuthed := s.isAuthenticated(r)

	s.renderPage(w, "assets", assetsData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc: "Shared files for " + group.DisplayName,
		},
		GroupKey:      group.GroupKey,
		GroupName:     group.DisplayName,
		CanonicalName: group.CanonicalName,
		Assets:        assets,
		AssetCategory: category,
		AssetSearch:   search,
		IsAuthed:      isAuthed,
	})
}

// handleAssetUpload handles file upload for a group.
func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request) {
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

	// Parse multipart form
	if err := r.ParseMultipartForm(s.maxAssetSize); err != nil {
		http.Error(w, "failed to parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get uploader email
	uploaderEmail := strings.TrimSpace(r.FormValue("email"))
	category := strings.TrimSpace(r.FormValue("category"))
	description := strings.TrimSpace(r.FormValue("description"))

	if !isValidAssetCategory(category) {
		category = "other"
	}

	// Get the uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file extension
	ext := fileExtension(header.Filename)
	if !isAllowedAssetExtension(ext) {
		http.Error(w, "file type not allowed", http.StatusBadRequest)
		return
	}

	// Check file size
	if header.Size > s.maxAssetSize {
		http.Error(w, "file too large", http.StatusBadRequest)
		return
	}

	// Sanitize filename and generate unique stored filename
	safeName := sanitizeFilename(header.Filename)
	storedName := generateToken()[:16] + "_" + safeName
	storedPath := filepath.Join(group.GroupKey, storedName)

	// Create the directory if needed
	fullPath := filepath.Join(s.assetDir, storedPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		log.Printf("web: create asset dir: %v", err)
		http.Error(w, "failed to store file", http.StatusInternalServerError)
		return
	}

	// Create the file
	dst, err := os.Create(fullPath)
	if err != nil {
		log.Printf("web: create asset file: %v", err)
		http.Error(w, "failed to store file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the file content
	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("web: write asset file: %v", err)
		http.Error(w, "failed to store file", http.StatusInternalServerError)
		return
	}

	// Save metadata to DB
	_, err = s.store.CreateAsset(GroupAsset{
		GroupKey:         group.GroupKey,
		UploadedByEmail: uploaderEmail,
		Filename:         storedName,
		OriginalFilename: header.Filename,
		MimeType:         mimeTypeForExt(ext),
		SizeBytes:        header.Size,
		Category:         category,
		Description:      description,
		StoredPath:       storedPath,
	})
	if err != nil {
		log.Printf("web: create asset record: %v", err)
		http.Error(w, "failed to save asset metadata", http.StatusInternalServerError)
		return
	}

	log.Printf("web: asset uploaded: %s for group %s (%d bytes)", storedName, group.GroupKey, header.Size)

	// Redirect back to assets page
	http.Redirect(w, r, "/groups/"+group.CanonicalName+"/assets", http.StatusSeeOther)
}

// handleAssetDownload serves a file download for a group asset.
func (s *Server) handleAssetDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	idStr := r.PathValue("id")
	if name == "" || idStr == "" {
		http.Error(w, "name and id required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid asset id", http.StatusBadRequest)
		return
	}

	asset, err := s.store.GetAsset(group.GroupKey, id)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	fullPath := filepath.Join(s.assetDir, asset.StoredPath)
	w.Header().Set("Content-Type", asset.MimeType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+asset.OriginalFilename+"\"")
	http.ServeFile(w, r, fullPath)
}

// handleAssetDelete deletes a group asset (file + metadata).
func (s *Server) handleAssetDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	idStr := r.PathValue("id")
	if name == "" || idStr == "" {
		http.Error(w, "name and id required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid asset id", http.StatusBadRequest)
		return
	}

	asset, err := s.store.GetAsset(group.GroupKey, id)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	// Delete the file from disk
	fullPath := filepath.Join(s.assetDir, asset.StoredPath)
	_ = os.Remove(fullPath)

	// Delete the metadata
	if err := s.store.DeleteAsset(group.GroupKey, id); err != nil {
		log.Printf("web: delete asset %d: %v", id, err)
	}

	http.Redirect(w, r, "/groups/"+group.CanonicalName+"/assets", http.StatusSeeOther)
}

// handleAssetEditDescription updates the description of a group asset.
func (s *Server) handleAssetEditDescription(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	idStr := r.PathValue("id")
	if name == "" || idStr == "" {
		http.Error(w, "name and id required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid asset id", http.StatusBadRequest)
		return
	}

	description := r.FormValue("description")

	if err := s.store.UpdateAssetDescription(id, description); err != nil {
		log.Printf("web: update asset description %d: %v", id, err)
	}

	http.Redirect(w, r, "/groups/"+group.CanonicalName+"/assets", http.StatusSeeOther)
}