// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/wscoble/federated-meetup/internal/notify"
)

// pushSubscribeRequest is the JSON body for POST /push/subscribe.
type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	UserEmail string `json:"user_email"`
}

// handlePushSubscribe stores a web push subscription for a user.
// POST /push/subscribe with JSON body:
// {"endpoint": "...", "keys": {"p256dh": "...", "auth": "..."}, "user_email": "..."}
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.pushMgr == nil {
		http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
		return
	}

	var req pushSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Endpoint == "" {
		http.Error(w, "endpoint is required", http.StatusBadRequest)
		return
	}
	if req.UserEmail == "" {
		http.Error(w, "user_email is required", http.StatusBadRequest)
		return
	}

	if err := s.pushMgr.StoreSubscription(
		req.UserEmail, req.Endpoint, req.Keys.P256dh, req.Keys.Auth,
		time.Now().Unix(),
	); err != nil {
		log.Printf("web: push subscribe error: %v", err)
		http.Error(w, "failed to store subscription", http.StatusInternalServerError)
		return
	}

	// If we have prefs, enable push for this user automatically.
	if s.prefs != nil {
		prefs, err := s.prefs.GetUserPrefs(req.UserEmail)
		if err == nil {
			prefs.NotifyPush = true
			_ = s.prefs.SetUserPrefs(prefs)
		}
	}

	log.Printf("web: push subscription stored for %s (endpoint=%s...)", req.UserEmail, truncate(req.Endpoint, 40))
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"status":"subscribed"}`))
}

// handlePushUnsubscribe removes a web push subscription.
// POST /push/unsubscribe with JSON body: {"endpoint": "..."}
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.pushMgr == nil {
		http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Endpoint == "" {
		http.Error(w, "endpoint is required", http.StatusBadRequest)
		return
	}

	if err := s.pushMgr.DeleteSubscription(req.Endpoint); err != nil {
		log.Printf("web: push unsubscribe error: %v", err)
		http.Error(w, "failed to delete subscription", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"unsubscribed"}`))
}

// handleVapidPublicKey returns the VAPID public key for the browser to
// use when subscribing to push notifications.
// GET /push/vapid-public-key
func (s *Server) handleVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.pushMgr == nil {
		http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"publicKey":%q}`, s.pushMgr.PublicKey())
}

// handleServiceWorker serves the service worker from the embedded static FS.
// GET /sw.js
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(staticFS, "sw.js")
	if err != nil {
		http.Error(w, "service worker not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

// ---- Notification preferences ----

// handleNotifyPrefs returns the notification preferences for the
// current user (identified by ?email= query param).
// GET /notifications/preferences?email=alice@example.com
func (s *Server) handleNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	if s.prefs == nil {
		http.Error(w, "notification preferences not configured", http.StatusServiceUnavailable)
		return
	}

	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}

	userPrefs, err := s.prefs.GetUserPrefs(email)
	if err != nil {
		http.Error(w, "failed to get preferences", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{
		"notify_email": userPrefs.NotifyEmail,
		"notify_sms":   userPrefs.NotifySMS,
		"notify_push":  userPrefs.NotifyPush,
	})
}

// handleSetNotifyPrefs updates the notification preferences for a user.
// POST /notifications/preferences with JSON body:
// {"user_email": "...", "notify_email": true, "notify_sms": false, "notify_push": true}
func (s *Server) handleSetNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	if s.prefs == nil {
		http.Error(w, "notification preferences not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		UserEmail   string `json:"user_email"`
		NotifyEmail bool   `json:"notify_email"`
		NotifySMS   bool   `json:"notify_sms"`
		NotifyPush  bool   `json:"notify_push"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.UserEmail == "" {
		http.Error(w, "user_email is required", http.StatusBadRequest)
		return
	}

	if err := s.prefs.SetUserPrefs(notify.UserNotifyPrefs{
		UserEmail:   req.UserEmail,
		NotifyEmail: req.NotifyEmail,
		NotifySMS:   req.NotifySMS,
		NotifyPush:  req.NotifyPush,
	}); err != nil {
		http.Error(w, "failed to set preferences", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"updated"}`))
}

// truncate returns the first n characters of s, with "..." appended
// if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}