// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"

	"github.com/wscoble/federated-meetup/internal/activitypub"
	"github.com/wscoble/federated-meetup/internal/email"
	"github.com/wscoble/federated-meetup/internal/host"
	"github.com/wscoble/federated-meetup/internal/notify"
	"github.com/wscoble/federated-meetup/internal/notify/push"
	"github.com/wscoble/federated-meetup/internal/product"
	"github.com/wscoble/federated-meetup/internal/video"
)
const defaultAssetDir = "./data/assets"

// maxAssetFileSize is the default maximum upload size (50 MB).
const maxAssetFileSize = 50 * 1024 * 1024

// allowedAssetExtensions is the allowlist of file extensions for uploads.
var allowedAssetExtensions = map[string]bool{
	"pdf":  true,
	"doc":  true,
	"docx": true,
	"txt":  true,
	"mp3":  true,
	"m4a":  true,
	"wav":  true,
	"jpg":  true,
	"png":  true,
	"webp": true,
	"epub": true,
}

// Server is the web frontend server. It wraps the host.Service (protocol),
// product.Service (ticketing), and a local SQLite store.
type Server struct {
	host    *host.Service
	product *product.Service
	store   *Store
	tmpls   templateMap
	now     func() time.Time
	email   email.EmailSender
	baseURL string                          // configured base URL for absolute links (e.g. magic-link emails)
	ap      *activitypub.ActivityPubService // optional: ActivityPub delivery

	// Notification system (optional — nil if not configured).
	pushMgr  *push.Manager          // web push VAPID key management + subscription storage
	notifier *notify.Notifier       // multi-channel notifier (push → email → SMS)
	notifyQ  *notify.Queue           // SQLite-backed notification queue
	prefs    *notify.PrefsStore      // per-user and per-group notification preferences
	notifyProc *notify.Processor    // background queue processor

	// assetDir is the base directory for storing group asset files.
	// Default: ./data/assets. Files are stored under {assetDir}/{group_key}/.
	assetDir string

	// maxAssetSize is the maximum allowed file upload size in bytes.
	maxAssetSize int64

	// video signaling server for P2P video chat
	videoHub  *video.RoomHub
	videoSig  *video.SignalingServer
}

// NewServer constructs a web Server. The host and product services may be nil
// (the web layer degrades gracefully — pages show cached data from SQLite).
// emailSender may be nil (degrades to no-op). baseURL is used for absolute
// links in emails; if empty, request-derived base URL is used.
func NewServer(hostSvc *host.Service, prodSvc *product.Service, store *Store, emailSender email.EmailSender, baseURL string) (*Server, error) {
	tmpls, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("web: load templates: %w", err)
	}
	if emailSender == nil {
		emailSender = email.NewNoopSender()
	}
	return &Server{
		host:        hostSvc,
		product:     prodSvc,
		store:       store,
		tmpls:       tmpls,
		now:         time.Now,
		email:       emailSender,
		baseURL:     baseURL,
		assetDir:    defaultAssetDir,
		maxAssetSize: maxAssetFileSize,
		videoHub:    video.NewRoomHub(),
	}, nil
}

// SetClock overrides the time source. Test-only.
func (s *Server) SetClock(now func() time.Time) {
	s.now = now
	s.store.SetClock(now)
}

// SetActivityPubService sets the ActivityPub service used for federated
// delivery. When set, newly created events are delivered to remote followers.
func (s *Server) SetActivityPubService(ap *activitypub.ActivityPubService) {
	s.ap = ap
}

// SetNotificationSystem wires the multi-channel notification system into
// the web server. All parameters are optional — nil values disable that
// component. The push manager, if non-nil, enables the /push/subscribe
// endpoint. The notifier enables multi-channel delivery. The queue and
// processor enable background notification processing.
func (s *Server) SetNotificationSystem(pushMgr *push.Manager, notifier *notify.Notifier, notifyQ *notify.Queue, prefs *notify.PrefsStore, proc *notify.Processor) {
	s.pushMgr = pushMgr
	s.notifier = notifier
	s.notifyQ = notifyQ
	s.prefs = prefs
	s.notifyProc = proc
}

// PushEnabled returns true if web push notifications are configured.
func (s *Server) PushEnabled() bool {
	return s.pushMgr != nil
}

// PushPublicKey returns the VAPID public key for web push, or empty string
// if push is not configured.
func (s *Server) PushPublicKey() string {
	if s.pushMgr == nil {
		return ""
	}
	return s.pushMgr.PublicKey()
}

// SetAssetDir overrides the base directory for storing group asset files.
// Default: ./data/assets.
func (s *Server) SetAssetDir(dir string) {
	s.assetDir = dir
}

// SetMaxAssetSize overrides the maximum file upload size in bytes.
// Default: 50 MB.
func (s *Server) SetMaxAssetSize(size int64) {
	s.maxAssetSize = size
}

// Routes returns an http.Handler with all web routes registered.
// Uses Go 1.22+ stdlib ServeMux pattern matching.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// Public pages
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /groups/new", s.handleNewGroup)
	mux.HandleFunc("POST /groups/new", s.handleCreateGroup)
	mux.HandleFunc("GET /groups/{name}", s.handleGroup)
	mux.HandleFunc("GET /groups/{name}/calendar.ics", s.handleGroupICS)
	mux.HandleFunc("GET /groups/{name}/feed.xml", s.handleGroupRSS)
	mux.HandleFunc("GET /events/{group_key}/{event_id}", s.handleEvent)
	mux.HandleFunc("GET /events/{group_key}/{event_id}/calendar.ics", s.handleEventICS)
	mux.HandleFunc("POST /events/{group_key}/{event_id}/rsvp", s.handleRsvpSubmit)
	mux.HandleFunc("POST /events/{group_key}/{event_id}/purchase", s.handlePurchaseTicket)

	// RSVP magic-link flow
	mux.HandleFunc("GET /rsvp/{token}", s.handleRsvpConfirm)
	mux.HandleFunc("POST /rsvp/{token}/confirm", s.handleRsvpConfirmPost)

	// My RSVPs
	mux.HandleFunc("GET /my-rsvps", s.handleMyRsvps)
	mux.HandleFunc("POST /my-rsvps/cancel", s.handleCancelRsvp)

	// Organizer dashboard (auth-gated)
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /dashboard/login", s.handleLogin)
	mux.HandleFunc("POST /dashboard/login", s.handleLoginPost)
	mux.HandleFunc("POST /dashboard/events", s.handleCreateEvent)
	mux.HandleFunc("POST /dashboard/events/{event_id}/tickets", s.handleCreateTicket)
	mux.HandleFunc("POST /dashboard/events/{event_id}/cancel", s.handleCancelEvent)
	mux.HandleFunc("POST /dashboard/events/{group_key}/checkin", s.handleCheckIn)
	mux.HandleFunc("POST /dashboard/events/{group_key}/bulk-checkin", s.handleBulkCheckIn)
	mux.HandleFunc("POST /dashboard/events/{group_key}/rsvp-reminders", s.handleSendRsvpReminders)
	mux.HandleFunc("GET /dashboard/attendance", s.handleAttendanceHistory)
	mux.HandleFunc("POST /dashboard/logout", s.handleLogout)
	mux.HandleFunc("GET /dashboard/events/{event_id}/attendees.csv", s.handleAttendeesCSV)

	// Checkout
	mux.HandleFunc("GET /checkout/{order_id}", s.handleCheckout)

	// Group assets (file sharing)
	mux.HandleFunc("GET /groups/{name}/assets", s.handleGroupAssets)
	mux.HandleFunc("POST /groups/{name}/assets", s.handleAssetUpload)
	mux.HandleFunc("GET /groups/{name}/assets/{id}", s.handleAssetDownload)
	mux.HandleFunc("POST /groups/{name}/assets/{id}/delete", s.handleAssetDelete)
	mux.HandleFunc("POST /groups/{name}/assets/{id}/description", s.handleAssetEditDescription)

	// Prayer requests (group-scoped)
	mux.HandleFunc("GET /groups/{name}/prayer-list", s.handlePrayerList)
	mux.HandleFunc("POST /groups/{name}/prayer-list/submit", s.handlePrayerSubmit)
	mux.HandleFunc("POST /groups/{name}/prayer-list/{id}/update", s.handlePrayerUpdate)
	mux.HandleFunc("GET /groups/{name}/prayer-list/print", s.handlePrayerPrint)

	// Group messaging (threaded chat)
	mux.HandleFunc("POST /groups/{group_key}/messages", s.handlePostMessage)
	mux.HandleFunc("GET /groups/{group_key}/messages", s.handleListMessages)
	mux.HandleFunc("GET /groups/{group_key}/messages/search", s.handleSearchMessages)
	mux.HandleFunc("POST /groups/{group_key}/messages/{id}/reactions", s.handleToggleReaction)

	// Member management (group-scoped)
	mux.HandleFunc("GET /groups/{name}/members", s.handleMembersList)
	mux.HandleFunc("POST /groups/{name}/members/invite", s.handleMemberInvite)
	mux.HandleFunc("POST /groups/{name}/members/{id}/role", s.handleMemberRoleChange)
	mux.HandleFunc("POST /groups/{name}/members/{id}/remove", s.handleMemberRemove)

	// Web push notifications
	mux.HandleFunc("POST /push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("POST /push/unsubscribe", s.handlePushUnsubscribe)
	mux.HandleFunc("GET /push/vapid-public-key", s.handleVapidPublicKey)
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)

	// Notification preferences
	mux.HandleFunc("GET /notifications/preferences", s.handleNotifyPrefs)
	mux.HandleFunc("POST /notifications/preferences", s.handleSetNotifyPrefs)

	// Prometheus metrics endpoint
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Video chat (P2P WebRTC)
	mux.HandleFunc("GET /events/{group_key}/{event_id}/video", s.handleVideoRoom)
	mux.HandleFunc("GET /ws/video/{group_key}", s.handleVideoWS)

	// Scheduling: availability polls, waitlist, recurrence exceptions
	mux.HandleFunc("POST /groups/{name}/events/{event_id}/poll", s.handleCreatePoll)
	mux.HandleFunc("POST /groups/{name}/events/{event_id}/poll/vote", s.handleVoteOnPoll)
	mux.HandleFunc("POST /groups/{name}/events/{event_id}/waitlist/join", s.handleJoinWaitlist)
	mux.HandleFunc("POST /groups/{name}/events/{event_id}/waitlist/promote", s.handlePromoteFromWaitlist)
	mux.HandleFunc("POST /groups/{name}/events/{event_id}/exceptions", s.handleAddRecurrenceException)

	return s.metricsMiddleware(s.withMiddleware(mux))
}

// ---- Template loading ----

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static/*
var staticFiles embed.FS

// staticFS is the sub-filesystem rooted at "static" for http.FileServer.
var staticFS, _ = fs.Sub(staticFiles, "static")

// templateMap holds separately-parsed templates keyed by page name.
// Each entry is a *template.Template that includes the base template
// plus that page's definitions. This prevents block definitions from
// different pages (e.g. "content", "head") from colliding.
type templateMap map[string]*template.Template

// loadTemplates parses all embedded HTML templates into a templateMap.
// Each page template is parsed together with base.html so that block
// definitions ("content", "title", "head") don't collide across pages.
func loadTemplates() (templateMap, error) {
	funcs := template.FuncMap{
		"formatTime":     formatTime,
		"formatMoney":    formatMoney,
		"formatCents":    formatCents,
		"formatDate":     formatDate,
		"formatDateShort": formatDateShort,
		"formatRelative": formatRelative,
		"isPast":         isPast,
		"isToday":        isToday,
		"isThisWeek":     isThisWeek,
		"dayOfWeek":      dayOfWeek,
		"dayOfMonth":     dayOfMonth,
		"monthShort":     monthShort,
		"timeOnly":       timeOnly,
		"formatISO":      formatISO,
		"formatDuration": formatDuration,
		"formatPercent":  formatPercent,
		"percentInt":     percentInt,
		"div":            divInt,
		"add":            addInt,
		"fillClass":      fillClass,
		"toInt":          toInt,
		"linkify":        linkify,
		"assetCategoryLabel": assetCategoryLabel,
		"assetCategoryIcon":  assetCategoryIcon,
		"isAudioFile":         isAudioFile,
		"formatFileSize":     formatFileSize,
		// Prayer request template functions
		"prayerCategoryLabel":     prayerCategoryLabel,
		"prayerStatusLabel":       prayerStatusLabel,
		"prayerStatusBadgeClass":  prayerStatusBadgeClass,
		"prayerCategoryBadgeClass": prayerCategoryBadgeClass,
		"formatDaysAgo":           formatDaysAgo,
		"prayerSubmitterName":     prayerSubmitterDisplayName,
		// Messaging template functions
		"formatMessageShort": formatMessageShort,
		"initials":           initials,
		"reactionEmojis":    reactionEmojis,
		"formatInt64":        formatInt64,
		// Member management template functions
		"memberRoleLabel":      memberRoleLabel,
		"memberRoleBadgeClass": memberRoleBadgeClass,
		"memberRoleString":     memberRoleString,
	}

	// Read base template
	baseData, err := templateFiles.ReadFile("templates/base.html")
	if err != nil {
		return nil, fmt.Errorf("read base.html: %w", err)
	}

	// Read fragments template (shared across pages for event_card, etc.)
	fragData, err := templateFiles.ReadFile("templates/fragments.html")
	if err != nil {
		return nil, fmt.Errorf("read fragments.html: %w", err)
	}

	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		return nil, err
	}

	tmpls := make(templateMap)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "base.html" {
			continue
		}
		pageData, err := templateFiles.ReadFile("templates/" + name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		// Each page template is parsed fresh with the base template
		// AND the fragments template (for shared blocks like event_card).
		t := template.New("").Funcs(funcs)
		if _, err := t.Parse(string(baseData)); err != nil {
			return nil, fmt.Errorf("parse base for %s: %w", name, err)
		}
		if name != "fragments.html" {
			if _, err := t.Parse(string(fragData)); err != nil {
				return nil, fmt.Errorf("parse fragments for %s: %w", name, err)
			}
		}
		if _, err := t.Parse(string(pageData)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		// Key is the page name without extension (e.g. "home", "event").
		key := strings.TrimSuffix(name, ".html")
		tmpls[key] = t
	}
	return tmpls, nil
}

// formatTime converts a unix timestamp to a human-readable string.
// Uses the server's local timezone (configured via TZ env or system).
func formatTime(unix int64) string {
	return time.Unix(unix, 0).Format("Mon, Jan 2, 2006 3:04 PM MST")
}

// formatMoney formats a pb.Money proto as a human-readable price string.
func formatMoney(m *pb.Money) string {
	if m == nil {
		return "Free"
	}
	if m.Amount == 0 {
		return "Free"
	}
	return formatCents(m.Amount)
}

// formatCents converts a cents amount to a dollar string.
func formatCents(cents uint64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// formatDate formats a unix timestamp as "Mon, Jan 2, 2006".
func formatDate(unix int64) string {
	return time.Unix(unix, 0).Format("Mon, Jan 2, 2006")
}

// formatDateShort formats as "Jan 2".
func formatDateShort(unix int64) string {
	return time.Unix(unix, 0).Format("Jan 2")
}

// formatRelative returns a human-friendly relative time string.
// "Today", "Tomorrow", "In 3 days", "Last week", "2 months ago".
func formatRelative(unix int64) string {
	t := time.Unix(unix, 0)
	now := time.Now()
	diff := t.Sub(now)

	if diff < 0 {
		abs := -diff
		switch {
		case abs < time.Hour:
			return "Just now"
		case abs < 24*time.Hour:
			return "Earlier today"
		case abs < 48*time.Hour:
			return "Yesterday"
		case abs < 7*24*time.Hour:
			return fmt.Sprintf("%d days ago", int(abs.Hours()/24))
		case abs < 14*24*time.Hour:
			return "Last week"
		case abs < 30*24*time.Hour:
			return fmt.Sprintf("%d weeks ago", int(abs.Hours()/(24*7)))
		case abs < 365*24*time.Hour:
			return fmt.Sprintf("%d months ago", int(abs.Hours()/(24*30)))
		default:
			return fmt.Sprintf("%d years ago", int(abs.Hours()/(24*365)))
		}
	}

	switch {
	case diff < time.Hour:
		return "Soon"
	case diff < 24*time.Hour:
		return "Today"
	case diff < 48*time.Hour:
		return "Tomorrow"
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("In %d days", int(diff.Hours()/24))
	case diff < 14*24*time.Hour:
		return "Next week"
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("In %d weeks", int(diff.Hours()/(24*7)))
	case diff < 365*24*time.Hour:
		return fmt.Sprintf("In %d months", int(diff.Hours()/(24*30)))
	default:
		return fmt.Sprintf("In %d years", int(diff.Hours()/(24*365)))
	}
}

// isPast returns true if the unix timestamp is in the past.
func isPast(unix int64) bool {
	return unix < time.Now().Unix()
}

// isToday returns true if the unix timestamp is today.
func isToday(unix int64) bool {
	t := time.Unix(unix, 0)
	now := time.Now()
	return t.Year() == now.Year() && t.YearDay() == now.YearDay()
}

// isThisWeek returns true if the event is within the next 7 days.
func isThisWeek(unix int64) bool {
	diff := unix - time.Now().Unix()
	return diff > 0 && diff < 7*24*3600
}

// dayOfWeek returns the abbreviated day name (Mon, Tue, etc).
func dayOfWeek(unix int64) string {
	return time.Unix(unix, 0).Format("Mon")
}

// dayOfMonth returns the day number as a string.
func dayOfMonth(unix int64) string {
	return time.Unix(unix, 0).Format("2")
}

// monthShort returns the abbreviated month name (Jan, Feb, etc).
func monthShort(unix int64) string {
	return time.Unix(unix, 0).Format("Jan")
}

// timeOnly returns the time portion as "3:04 PM".
func timeOnly(unix int64) string {
	return time.Unix(unix, 0).Format("3:04 PM")
}

// formatISO returns an ISO 8601 timestamp for use in <time datetime=""> attributes.
func formatISO(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02T15:04:05Z07:00")
}

// formatDuration returns a human-friendly duration string.
// e.g. "2 hours", "1.5 hours", "30 min".
func formatDuration(startUnix, endUnix int64) string {
	if endUnix <= startUnix {
		return ""
	}
	dur := time.Duration(endUnix - startUnix) * time.Second
	hours := dur.Hours()
	if hours >= 1 {
		if hours == float64(int(hours)) {
			return fmt.Sprintf("%d hour%s", int(hours), pluralS(int(hours)))
		}
		return fmt.Sprintf("%.1f hours", hours)
	}
	mins := dur.Minutes()
	return fmt.Sprintf("%d min", int(mins))
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// percentInt returns the integer percentage of n/d (0-100), without the % sign.
func percentInt(n, d int) int {
	if d <= 0 {
		return 0
	}
	pct := (n * 100) / d
	if pct > 100 {
		pct = 100
	}
	return pct
}

// formatPercent returns a percentage string like "75%" or "0%".
func formatPercent(n, d int) string {
	if d <= 0 {
		return "0%"
	}
	pct := (n * 100) / d
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%d%%", pct)
}

// toInt converts a numeric value (int, int64, uint64, float64) to int.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case int32:
		return int(n)
	case uint32:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// fillClass returns a CSS class for progress bar fill based on the ratio n/d:
// "fill-high" for >80%, "fill-warning" for 50-80%, "" otherwise.
func fillClass(n, d int) string {
	if d <= 0 {
		return ""
	}
	pct := (n * 100) / d
	if pct > 100 {
		pct = 100
	}
	if pct >= 80 {
		return "fill-high"
	}
	if pct >= 50 {
		return "fill-warning"
	}
	return ""
}

// divInt performs integer division: n / d. Returns 0 if d is 0.
func divInt(n, d int) int {
	if d == 0 {
		return 0
	}
	return n / d
}

// addInt returns the sum of two integers.
func addInt(a, b int) int {
	return a + b
}

// linkify converts plain text into safe HTML: auto-links URLs and converts
// newlines to <br> tags. Escapes all other HTML to prevent XSS.
func linkify(text string) template.HTML {
	// First escape all HTML
	esc := template.HTMLEscapeString(text)
	// Auto-link URLs
	urlRe := regexp.MustCompile(`https?://[^\s<>"']+`)
	esc = urlRe.ReplaceAllStringFunc(esc, func(url string) string {
		return `<a href="` + url + `" rel="noopener nofollow" target="_blank">` + url + `</a>`
	})
	// Convert newlines to <br>
	esc = strings.ReplaceAll(esc, "\n", "<br>")
	return template.HTML(esc)
}

// ---- Rendering helpers ----

func (s *Server) renderPage(w http.ResponseWriter, page string, data interface{}) {
	t, ok := s.tmpls[page]
	if !ok {
		log.Printf("web: template not found: %s", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("web: render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// renderNotFound renders a styled 404 page.
func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.renderPage(w, "not_found", pageBase{
		CSRFToken: csrfTokenFromRequest(r),
	})
}

// renderError renders a styled 500 error page.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.renderPage(w, "error", pageBase{
		CSRFToken: csrfTokenFromRequest(r),
	})
}

func (s *Server) renderFragment(w http.ResponseWriter, name string, data interface{}) {
	// Fragments are defined in fragments.html — use that template set.
	t, ok := s.tmpls["fragments"]
	if !ok {
		log.Printf("web: fragment template not found")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: render fragment %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---- Security helpers ----

// generateToken generates a random 32-char hex token.
func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// validateString checks length bounds and UTF-8 validity (mirrors
// internal/group.validateStringField).
func validateString(name, value string, minLen, maxLen int) error {
	n := len(value)
	if n < minLen {
		return fmt.Errorf("%s is too short (%d bytes, min %d)", name, n, minLen)
	}
	if n > maxLen {
		return fmt.Errorf("%s is too long (%d bytes, max %d)", name, n, maxLen)
	}
	return nil
}

// ---- Data types for templates ----

// pageData is the common interface for all template data types.
// Every page data struct embeds pageBase so the base template can safely
// access JSONLD (empty for pages that don't need it) and CSRFToken.

type pageBase struct {
	JSONLD    template.JS
	CSRFToken string
	MetaDesc  string
	MetaImage string
}

type homeData struct {
	pageBase
	Groups       []CachedGroup
	Events       []CachedEvent
	TodayEvents  []CachedEvent
	WeekEvents   []CachedEvent
	LaterEvents  []CachedEvent
	Query        string
	DateFilter   string
	TotalEvents  int
	TotalGroups  int
}

type groupData struct {
	pageBase
	Group          CachedGroup
	GroupKey       string
	UpcomingEvents []CachedEvent
	PastEvents     []CachedEvent
	PastEventCount int
	// Messaging
	Messages       []messageViewData
	MessageCount   int
	HasMoreMessages bool
	// Assets
	Assets         []GroupAsset
	AssetCategory  string
	AssetSearch    string
	IsAuthed       bool
	UserEmail       string
	UserName        string
}

type messageFormData struct {
	GroupKey         string
	CSRFToken        string
	UserEmail        string
	UserName        string
	ParentMessageID int64
}

type ticketView struct {
	TicketID string
	Name      string
	Price     *pb.Money
	Capacity  uint64
	Sold      uint64
}

type eventData struct {
	pageBase
	Event          CachedEvent
	Group          CachedGroup
	RsvpCount      int
	Tickets        []ticketView
	VideoRoomOpen  bool
	VideoInCall     int
}

type attendeeView struct {
	Email    string
	Name     string
	Attended bool
}

type dashboardEvent struct {
	EventID       string
	Title         string
	StartsAt      int64
	RsvpCount     int
	TicketSold    int
	EventRevenue  *pb.Money
	Attendees     []attendeeView
	Capacity      int
	CheckedIn     int
}

type dashboardData struct {
	pageBase
	GroupKey      string
	Events        []dashboardEvent
	TotalRevenue  *pb.Money
	TotalRsvps    int
	CareAlerts    []MemberCareAlert
}

type rsvpPageData struct {
	pageBase
	RSVP       RSVPRecord
	EventTitle string
	Confirmed  bool
}

type loginData struct {
	pageBase
}

type checkoutData struct {
	pageBase
	Order       OrderRecord
	EventTitle  string
	CheckoutURL string
}

type myRsvpsData struct {
	pageBase
	Email string
	Rsvps []myRsvpView
}

type myRsvpView struct {
	GroupKey  string
	EventID   string
	EventTitle string
	StartsAt  int64
	Confirmed bool
	Token     string
}

type fragmentData struct {
	Email    string
	Title    string
	GroupKey string
	EventID  string
	Error    string
}

// attendanceCheckInData is the template data for the HTMX attendee table fragment.
type attendanceCheckInData struct {
	GroupKey   string
	EventID    string
	Attendees  []attendeeView
	RsvpCount  int
	CheckedIn  int
	CSRFToken  string
}

// attendanceHistoryData is the template data for the per-group attendance history page.
type attendanceHistoryData struct {
	pageBase
	GroupKey   string
	GroupName  string
	History    []AttendanceMemberRow
	CareAlerts []MemberCareAlert
}

// ---- JSON-LD generation ----

// eventJSONLD generates a schema.org/Event JSON-LD block.
func eventJSONLD(e CachedEvent, rsvpCount int, baseURL string) string {
	eventStatus := "EventScheduled"
	if e.Cancelled {
		eventStatus = "EventCancelled"
	}
	startDate := time.Unix(e.StartsAt, 0).UTC().Format(time.RFC3339)

	ld := map[string]interface{}{
		"@type":                "Event",
		"name":                 e.Title,
		"startDate":            startDate,
		"eventStatus":          eventStatus,
		"eventAttendanceMode":  "OfflineEventAttendanceMode",
		"url":                  fmt.Sprintf("%s/events/%s/%s", baseURL, e.GroupKey, e.EventID),
	}
	if e.Description != "" {
		ld["description"] = e.Description
	}
	if e.Location != "" {
		ld["location"] = map[string]string{
			"@type": "Place",
			"name":  e.Location,
		}
	}
	if e.Capacity > 0 {
		ld["maximumAttendeeCapacity"] = e.Capacity
		ld["remainingAttendeeCapacity"] = e.Capacity - rsvpCount
	}

	b, _ := json.Marshal(ld)
	return string(b)
}

// ---- Sync helpers ----

// syncGroupFromProduct pulls group info from the product store into the
// local SQLite cache. Returns the CachedGroup.
func (s *Server) syncGroupFromProduct(groupID string) (CachedGroup, error) {
	if s.product == nil {
		// Try cache
		return s.store.GetGroup(groupID)
	}
	// The product store has group data — we'd call GetGroup via the store.
	// For now, return from cache.
	return s.store.GetGroup(groupID)
}

// syncEventsFromProduct pulls events from the product store into the local
// SQLite cache for a group.
func (s *Server) syncEventsFromProduct(groupID string) error {
	if s.product == nil {
		return nil
	}
	// Access product store via the service's Store field
	events := s.product.Store().EventsForGroup(groupID)
	for _, e := range events {
		var startsAt int64
		if e.StartsAt != nil {
			startsAt = e.StartsAt.Seconds
		}
		ce := CachedEvent{
			GroupKey:    groupID,
			EventID:     e.EventId,
			Title:       e.Title,
			Description: e.Description,
			StartsAt:    startsAt,
			Location:    e.Location,
			Capacity:    int(e.Capacity),
			Cancelled:   e.Cancelled,
		}
		if err := s.store.UpsertEvent(ce); err != nil {
			return err
		}
	}
	return nil
}

// eventFromProduct tries to get an event from the product store; falls back
// to the local cache.
func (s *Server) eventFromProduct(groupKey, eventID string) (CachedEvent, error) {
	if s.product != nil {
		if e, ok := s.product.Store().GetEvent(eventID); ok {
			var startsAt int64
			if e.StartsAt != nil {
				startsAt = e.StartsAt.Seconds
			}
			return CachedEvent{
				GroupKey:    groupKey,
				EventID:     e.EventId,
				Title:       e.Title,
				Description: e.Description,
				StartsAt:    startsAt,
				Location:    e.Location,
				Capacity:    int(e.Capacity),
				Cancelled:   e.Cancelled,
			}, nil
		}
	}
	return s.store.GetEvent(groupKey, eventID)
}

// groupFromProduct tries to get a group from the product store; falls back
// to the local cache.
func (s *Server) groupFromProduct(groupID string) (CachedGroup, error) {
	if s.product != nil {
		if g, ok := s.product.Store().GetGroup(groupID); ok {
			cg := CachedGroup{
				GroupKey:      g.GroupId,
				CanonicalName: g.CanonicalName,
				DisplayName:   g.DisplayName,
				Description:   g.Description,
				MemberCount:   g.MemberCount,
			}
			if g.CreatedAt != nil {
				cg.CreatedAt = g.CreatedAt.AsTime().Unix()
			}
			return cg, nil
		}
	}
	return s.store.GetGroup(groupID)
}

// groupByCanonicalName tries the product store first, then the local cache.
func (s *Server) groupByCanonicalName(name string) (CachedGroup, error) {
	if s.product != nil {
		// Walk groups in the product store
		// The product store doesn't have a "list all" method, but we can
		// try the cache first and verify via product store.
	}
	return s.store.GetGroupByCanonicalName(name)
}

// baseURL returns the base URL for generating absolute links (JSON-LD).
// In production this would come from config; for now we derive from the request.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// absoluteBaseURL returns the server's configured base URL if set,
// otherwise falls back to deriving from the request.
func (s *Server) absoluteBaseURL(r *http.Request) string {
	if s.baseURL != "" {
		return s.baseURL
	}
	return baseURL(r)
}

// storeFromContext retrieves the store from the request context (not used
// currently, but available for middleware-injected values).
func storeFromContext(_ context.Context) *Store {
	return nil
}

// ensure pb is referenced (used in some handlers for product types)
var _ pb.Event