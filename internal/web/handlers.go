// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ---- Public pages ----

// handleHome renders the home page: list of groups + upcoming events.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	groups, _ := s.store.ListGroups()
	events, _ := s.store.ListUpcomingEvents("", 10)

	// Sync from product store if available
	if s.product != nil {
		// Try to enrich events from the product store
		for i, e := range events {
			if pe, ok := s.product.Store().GetEvent(e.EventID); ok {
				events[i].Title = pe.Title
				events[i].Description = pe.Description
				if pe.Location != "" {
					events[i].Location = pe.Location
				}
				events[i].Capacity = int(pe.Capacity)
				events[i].Cancelled = pe.Cancelled
			}
		}
	}

	s.renderPage(w, "home", homeData{Groups: groups, Events: events})
}

// handleGroup renders a group profile page with upcoming events.
func (s *Server) handleGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "group name required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	events, _ := s.store.ListUpcomingEvents(group.GroupKey, 20)

	// Sync from product store
	if s.product != nil {
		for i, e := range events {
			if pe, ok := s.product.Store().GetEvent(e.EventID); ok {
				events[i].Title = pe.Title
				events[i].Description = pe.Description
				if pe.Location != "" {
					events[i].Location = pe.Location
				}
				events[i].Capacity = int(pe.Capacity)
				events[i].Cancelled = pe.Cancelled
			}
		}
	}

	s.renderPage(w, "group", groupData{Group: group, Events: events})
}

// handleEvent renders a single event page with schema.org/Event JSON-LD.
func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	eventID := r.PathValue("event_id")
	if groupKey == "" || eventID == "" {
		http.Error(w, "group_key and event_id required", http.StatusBadRequest)
		return
	}

	event, err := s.eventFromProduct(groupKey, eventID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rsvpCount, _ := s.store.RsvpCount(groupKey, eventID)

	jsonld := eventJSONLD(event, rsvpCount, baseURL(r))

	s.renderPage(w, "event", eventData{
		pageBase: pageBase{
			JSONLD:    jsonld,
			CSRFToken: csrfTokenFromRequest(r),
		},
		Event:     event,
		RsvpCount: rsvpCount,
	})
}

// handleRsvpSubmit handles the RSVP form submission from the event page.
// Creates a RSVP with a magic-link token and returns an HTMX fragment.
func (s *Server) handleRsvpSubmit(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	eventID := r.PathValue("event_id")
	if groupKey == "" || eventID == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing event identifier"})
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

	// Verify event exists
	if _, err := s.eventFromProduct(groupKey, eventID); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "event not found"})
		return
	}

	token := generateToken()
	rsvp := RSVPRecord{
		GroupKey:  groupKey,
		EventID:   eventID,
		UserEmail: email,
		UserName:  name,
		Token:     token,
		Confirmed: false,
		CreatedAt: s.now().Unix(),
	}
	if err := s.store.CreateRsvp(rsvp); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to create RSVP"})
		return
	}

	// Stub email: log the magic link
	magicLink := fmt.Sprintf("/rsvp/%s", token)
	log.Printf("web: RSVP magic link for %s: %s (event %s/%s)", email, magicLink, groupKey, eventID)

	s.renderFragment(w, "rsvp_fragment", fragmentData{Email: email})
}

// ---- RSVP magic-link flow ----

// handleRsvpConfirm renders the RSVP confirmation page when an attendee
// clicks the magic link.
func (s *Server) handleRsvpConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	rsvp, err := s.store.GetRsvpByToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get event title
	eventTitle := "Unknown event"
	if event, err := s.eventFromProduct(rsvp.GroupKey, rsvp.EventID); err == nil {
		eventTitle = event.Title
	}

	s.renderPage(w, "rsvp", rsvpPageData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		RSVP:       rsvp,
		EventTitle: eventTitle,
		Confirmed:  rsvp.Confirmed,
	})
}

// handleRsvpConfirmPost handles the POST to confirm an RSVP.
// Returns an HTMX fragment.
func (s *Server) handleRsvpConfirmPost(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "missing token"})
		return
	}

	rsvp, err := s.store.ConfirmRsvp(token)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid or expired token"})
		return
	}

	log.Printf("web: RSVP confirmed for %s (event %s/%s)", rsvp.UserEmail, rsvp.GroupKey, rsvp.EventID)
	s.renderFragment(w, "rsvp_confirmed_fragment", fragmentData{})
}

// ---- Organizer dashboard ----

// handleDashboard renders the organizer dashboard (auth-gated).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	// Sync events from product store
	_ = s.syncEventsFromProduct(groupKey)

	events, _ := s.store.ListEventsByGroup(groupKey)

	var dashEvents []dashboardEvent
	for _, e := range events {
		count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
		dashEvents = append(dashEvents, dashboardEvent{
			EventID:   e.EventID,
			Title:     e.Title,
			StartsAt:  e.StartsAt,
			RsvpCount: count,
		})
	}

	s.renderPage(w, "dashboard", dashboardData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		GroupKey: groupKey,
		Events:   dashEvents,
	})
}

// handleLogin renders the organizer login form.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "login", loginData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
	})
}

// handleLoginPost processes the organizer login form.
// Validates the organizer token against the product store and creates
// a session.
func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	if token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}

	// Validate against product store
	if s.product == nil {
		http.Error(w, "organizer auth not configured", http.StatusInternalServerError)
		return
	}

	// Find the group this token is valid for
	groupID, ok := s.product.Store().GetOrganizerTokenGroup(token)
	if !ok {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Create a session
	sessionToken := generateToken()
	if err := s.store.CreateSession(sessionToken, groupID, 7*24*time.Hour); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, sessionToken)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// handleLogout clears the organizer session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleCreateEvent handles the create event form (HTMX fragment response).
func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	startsAtStr := r.FormValue("starts_at")
	location := r.FormValue("location")
	capacityStr := r.FormValue("capacity")

	// Validate
	if err := validateString("title", title, 1, 1024); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("description", description, 0, 4096); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}
	if err := validateString("location", location, 0, 1024); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	// Parse start time
	startsAt, err := time.Parse("2006-01-02T15:04", startsAtStr)
	if err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid start time"})
		return
	}

	capacity, err := strconv.Atoi(capacityStr)
	if err != nil || capacity < 0 {
		capacity = 0
	}

	// Generate event ID
	eventID := generateToken()[:16]

	// Store in SQLite cache
	ce := CachedEvent{
		GroupKey:    groupKey,
		EventID:     eventID,
		Title:       title,
		Description: description,
		StartsAt:    startsAt.Unix(),
		Location:    location,
		Capacity:    capacity,
		Cancelled:   false,
	}
	if err := s.store.UpsertEvent(ce); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: "failed to create event"})
		return
	}

	// Also store in product store if available
	if s.product != nil {
		event := &pb.Event{
			EventId:     eventID,
			GroupId:     groupKey,
			Title:       title,
			Description: description,
			StartsAt:    timestamppb.New(startsAt),
			Location:    location,
			Capacity:    uint64(capacity),
			Paid:        false,
			Slug:        eventID,
		}
		s.product.Store().PutEvent(event)
	}

	log.Printf("web: event created: %s/%s — %s", groupKey, eventID, title)
	s.renderFragment(w, "event_created_fragment", fragmentData{
		Title:    title,
		GroupKey: groupKey,
		EventID:  eventID,
	})
}

// ---- Checkout ----

// handleCheckout redirects to the Stripe checkout URL for an order.
func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("order_id")
	if orderID == "" {
		http.Error(w, "order_id required", http.StatusBadRequest)
		return
	}

	// Try local store first
	order, err := s.store.GetOrder(orderID)
	if err == nil && order.StripeSessionID != "" {
		// Redirect to Stripe checkout URL
		checkoutURL := fmt.Sprintf("https://checkout.stripe.com/c/pay/%s", order.StripeSessionID)
		http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
		return
	}

	// Try product store
	if s.product != nil {
		if prodOrder, ok := s.product.Store().GetOrder(orderID); ok {
			if prodOrder.StripeSessionId != "" {
				// Store locally for future reference
				var amount int
				if prodOrder.AmountPaid != nil {
					amount = int(prodOrder.AmountPaid.Amount)
				}
				_ = s.store.CreateOrder(OrderRecord{
					OrderID:         prodOrder.OrderId,
					GroupKey:        "",
					EventID:         "",
					Email:           prodOrder.AttendeeEmail,
					AmountCents:     amount,
					Status:          prodOrder.Status.String(),
					StripeSessionID: prodOrder.StripeSessionId,
				})
				checkoutURL := fmt.Sprintf("https://checkout.stripe.com/c/pay/%s", prodOrder.StripeSessionId)
				http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
				return
			}
		}
	}

	http.NotFound(w, r)
}

// ---- Utility ----

// hexEncode is a convenience function.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}