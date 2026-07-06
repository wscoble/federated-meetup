// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"

	emailpkg "github.com/wscoble/federated-meetup/internal/email"
	productrecurrence "github.com/wscoble/federated-meetup/internal/product"
)

// myRsvpsSessionKey is the query-string key for the /my-rsvps magic-link
// session token. Surfaces in URLs — that's the design: the token is the
// credential, the URL is the side channel, and the bound on damage is
// the 24h TTL + single-use enforcement in the storage layer.
const myRsvpsSessionKey = "token"

// clientIP extracts the best-effort client IP from the request.
// Honors CF-Connecting-IP (Cloudflare), then X-Forwarded-For (first
// IP, if set), and finally r.RemoteAddr via net.SplitHostPort. If
// splitting RemoteAddr fails, the raw value is returned so the rate
// limiter can still key on it (worst case: per-connection rather than
// per-client, which is only a problem if many users share a NAT).
func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// First entry is the original client; comma-separated list.
		if i := strings.Index(v, ","); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- Public pages ----

// handleHome renders the home page: list of groups + upcoming events.
// Supports ?q= search query to filter groups and events by name.
// Supports ?when= filter: "today", "week", "all" (default: all).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	groups, _ := s.store.ListGroups()
	events, _ := s.store.ListUpcomingEvents("", 50)

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
			// Populate RSVP count for each event
			count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
			events[i].RsvpCount = count
		}
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	dateFilter := strings.TrimSpace(r.URL.Query().Get("when"))
	if dateFilter == "" {
		dateFilter = "all"
	}

	if query != "" {
		// Filter groups by display name (case-insensitive)
		var filteredGroups []CachedGroup
		for _, g := range groups {
			if strings.Contains(strings.ToLower(g.DisplayName), strings.ToLower(query)) {
				filteredGroups = append(filteredGroups, g)
			}
		}
		groups = filteredGroups

		// Filter events by title (case-insensitive)
		var filteredEvents []CachedEvent
		for _, e := range events {
			if strings.Contains(strings.ToLower(e.Title), strings.ToLower(query)) {
				filteredEvents = append(filteredEvents, e)
			}
		}
		events = filteredEvents
	}

	// Split events into date groups for the default view.
	var today, thisWeek, later []CachedEvent
	now := time.Now().Unix()
	for _, e := range events {
		if e.Cancelled {
			later = append(later, e)
			continue
		}
		diff := e.StartsAt - now
		if diff < 0 {
			continue // skip past events in home page
		}
		// Populate RSVP count
		count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
		e.RsvpCount = count
		if diff < 24*3600 {
			today = append(today, e)
		} else if diff < 7*24*3600 {
			thisWeek = append(thisWeek, e)
		} else {
			later = append(later, e)
		}
	}

	// Apply date filter to the events list
	var displayEvents []CachedEvent
	switch dateFilter {
	case "today":
		displayEvents = today
	case "week":
		displayEvents = append(append(displayEvents, today...), thisWeek...)
	default:
		displayEvents = events
	}

	s.renderPage(w, "home", homeData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc:  "Discover community events across the federation. Find meetups, workshops, and hackathons near you.",
		},
		Groups: groups, Events: displayEvents,
		TodayEvents: today, WeekEvents: thisWeek, LaterEvents: later,
		Query: query, DateFilter: dateFilter, TotalEvents: len(events),
		TotalGroups: len(groups),
	})
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
		s.renderNotFound(w, r)
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

	// Split into upcoming and past.
	now := s.now().Unix()
	var upcoming, past []CachedEvent
	for _, e := range events {
		// Populate RSVP count
		count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
		e.RsvpCount = count
		if e.StartsAt < now {
			past = append(past, e)
		} else {
			upcoming = append(upcoming, e)
		}
	}

	s.renderPage(w, "group", groupData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc:  group.Description,
		},
		Group:          group,
		GroupKey:       group.GroupKey,
		UpcomingEvents: upcoming,
		PastEvents:     past,
		PastEventCount: len(past),
	})
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
		s.renderNotFound(w, r)
		return
	}

	rsvpCount, _ := s.store.RsvpCount(groupKey, eventID)

	// Get group info for organizer display
	group, err := s.groupFromProduct(groupKey)
	if err != nil {
		group = CachedGroup{GroupKey: groupKey, DisplayName: groupKey}
	}

	// Fetch tickets from product store
	var tickets []ticketView
	if s.product != nil {
		ptickets := s.product.Store().TicketsForEvent(eventID)
		for _, t := range ptickets {
			tickets = append(tickets, ticketView{
				TicketID: t.TicketId,
				Name:      t.Name,
				Price:     t.Price,
				Capacity:  t.Capacity,
				Sold:      t.Sold,
			})
		}
	}

	jsonld := eventJSONLD(event, rsvpCount, baseURL(r))

	s.renderPage(w, "event", eventData{
		pageBase: pageBase{
			JSONLD:    template.JS(jsonld),
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc:  event.Description,
		},
		Event:     event,
		Group:     group,
		RsvpCount: rsvpCount,
		Tickets:   tickets,
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

	// Build the absolute magic-link URL using the configured base URL.
	base := s.absoluteBaseURL(r)
	magicLink := fmt.Sprintf("%s/rsvp/%s", base, token)

	// Fetch event details for the email.
	eventTitle := "your event"
	eventLocation := ""
	var eventStartsAt int64
	if event, err := s.eventFromProduct(groupKey, eventID); err == nil {
		eventTitle = event.Title
		eventLocation = event.Location
		eventStartsAt = event.StartsAt
	}
	groupName := groupKey
	if g, err := s.groupFromProduct(groupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	// Render and send the RSVP confirmation email.
	subject, body, renderErr := emailpkg.RenderRsvpConfirm(emailpkg.RsvpConfirmData{
		EventTitle:    eventTitle,
		EventDate:     emailpkg.FormatEventDate(eventStartsAt),
		EventLocation: eventLocation,
		MagicLink:     magicLink,
		GroupName:     groupName,
	})
	if renderErr != nil {
		log.Printf("web: render rsvp_confirm email: %v", renderErr)
	} else if err := s.email.Send(r.Context(), email, subject, body); err != nil {
		log.Printf("web: send rsvp_confirm email to %s: %v", email, err)
	}

	s.renderFragment(w, "rsvp_fragment", fragmentData{Email: email})
}

// ---- Ticket purchase flow ----

// handlePurchaseTicket creates an order via product.Service.PurchaseTicket
// and redirects to the checkout page.
func (s *Server) handlePurchaseTicket(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	eventID := r.PathValue("event_id")
	if groupKey == "" || eventID == "" {
		http.Error(w, "missing event identifier", http.StatusBadRequest)
		return
	}

	ticketID := r.FormValue("ticket_id")
	email := r.FormValue("email")

	if ticketID == "" {
		http.Error(w, "ticket_id required", http.StatusBadRequest)
		return
	}
	if err := validateString("email", email, 3, 256); err != nil {
		http.Error(w, "valid email required", http.StatusBadRequest)
		return
	}

	if s.product == nil {
		http.Error(w, "ticketing not configured", http.StatusInternalServerError)
		return
	}

	// Use the product store's atomic purchase directly
	ps := s.product.Store()
	ticket, ok := ps.GetTicket(ticketID)
	if !ok {
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	}

	// Compute amount
	var amount uint64
	currency := "USD"
	if ticket.Price != nil {
		amount = ticket.Price.Amount
		if ticket.Price.Currency != "" {
			currency = ticket.Price.Currency
		}
	}

	// Create order via AtomicPurchaseTicket
	orderID := generateToken()[:16]
	sessionID := fmt.Sprintf("mock_sess_%s", orderID)

	order, found, soldOut := ps.AtomicPurchaseTicket(ticketID, email, orderID, sessionID, 1, amount, currency)
	if !found {
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	}
	if soldOut {
		http.Error(w, "ticket is sold out", http.StatusConflict)
		return
	}

	// Store order in SQLite for the checkout page
	var amountInt int
	if order.AmountPaid != nil {
		amountInt = int(order.AmountPaid.Amount)
	}
	_ = s.store.CreateOrder(OrderRecord{
		OrderID:         order.OrderId,
		GroupKey:        groupKey,
		EventID:         eventID,
		Email:           email,
		AmountCents:     amountInt,
		Status:          order.Status.String(),
		StripeSessionID: order.StripeSessionId,
	})

	log.Printf("web: ticket purchased — order %s for event %s/%s by %s", order.OrderId, groupKey, eventID, email)
	http.Redirect(w, r, "/checkout/"+order.OrderId, http.StatusSeeOther)
}

// ---- My RSVPs (anti-dox magic-link flow) ----
//
// The /my-rsvps endpoint is gated by a magic-link session token bound
// to a specific email. Knowledge of the email alone is NOT a credential
// — that was the original IDOR-by-email bug. The flow is:
//
//   1. GET  /my-rsvps              → email-entry form (no token).
//   2. POST /my-rsvps/login {email} → issues a single-use session
//                                     token, sends the magic link via
//                                     the inbox, renders "check your
//                                     email" (no enumeration).
//   3. GET  /my-rsvps?token=X      → validates the session token,
//                                     lists the user's RSVPs, then
//                                     burns the session (single-use).
//   4. POST /my-rsvps/logout       → drops the current session token
//                                     and re-renders the form.
//   5. POST /my-rsvps/cancel {rsvp_token} → burns the rsvp_token and
//                                     cancels the matching RSVP.
//
// See SECURITY.md, surfaces #1 (dox-by-attendance), #3 (enumeration /
// harassment), #4 (magic-link replay), #5 (CSRF), and the anti-dox
// skill. All seven checks are addressed by this flow.

// handleMyRsvps renders the /my-rsvps page in one of four states:
//   1. No token: email-entry form (CSRFToken populated).
//   2. Token invalid / expired / already-used: form + "link invalid"
//      hint (LinkInvalid: true).
//   3. Token valid + RSVPs: list view (SessionOK: true, Email: X).
//   4. POST /my-rsvps/login just ran: "check your email" (LinkSentTo).
func (s *Server) handleMyRsvps(w http.ResponseWriter, r *http.Request) {
	data := myRsvpsData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
	}

	token := r.URL.Query().Get(myRsvpsSessionKey)
	if token == "" {
		// State 1: email-entry form. Nothing else to set.
		s.renderPage(w, "my_rsvps", data)
		return
	}

	// Consume the session atomically (read + burn in one round-trip).
	// This is the correct ordering for a single-use session: the
	// moment we authenticate the user, the session is burned. If
	// rendering then fails, the user just requests a new link —
	// which is the desired single-use semantic. The atomic
	// DELETE...RETURNING also avoids the connection-pool deadlock
	// that the previous read-then-delete pattern caused.
	sess, err := s.store.ConsumeMyRsvpsSession(token)
	if err != nil {
		// State 2: token did not validate. The handler does NOT
		// enumerate (it does not say "no such email" vs "expired" —
		// both look the same to the attacker).
		data.LinkInvalid = true
		s.renderPage(w, "my_rsvps", data)
		return
	}

	// List the user's RSVPs, inflate event titles.
	records, _ := s.store.ListRsvpsByEmail(sess.Email)
	var views []myRsvpView
	for _, rec := range records {
		eventTitle := "Unknown event"
		var startsAt int64
		if event, err := s.eventFromProduct(rec.GroupKey, rec.EventID); err == nil {
			eventTitle = event.Title
			startsAt = event.StartsAt
		}
		views = append(views, myRsvpView{
			GroupKey:   rec.GroupKey,
			EventID:    rec.EventID,
			EventTitle: eventTitle,
			StartsAt:   startsAt,
			Confirmed:  rec.Confirmed,
			Token:      rec.Token,
		})
	}

	data.SessionOK = true
	data.Email = sess.Email
	data.Rsvps = views
	s.renderPage(w, "my_rsvps", data)
}

// handleMyRsvpsLogin issues a magic-link session for the given email
// and sends the link via the inbox. The handler is intentionally
// non-enumerating: it always renders the same "check your email" page
// and never reveals whether the email is registered or whether the
// email was actually sent.
//
// Rate-limited per IP (0.2 req/s, burst 5) to defeat bulk
// enumeration / inbox-spam. The rate is per SECURITY.md "Email
// enumeration / harassment" (surface #3).
func (s *Server) handleMyRsvpsLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.ipAllow(ip, 0.2, 5) {
		http.Error(w, "too many requests, slow down", http.StatusTooManyRequests)
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if err := validateString("email", email, 3, 256); err != nil {
		// Validation failure is the same UX as success (no enumeration),
		// but we do show the form again rather than the "check your
		// email" page — an empty email is not a real identifier, so
		// the user needs to fix the input.
		s.renderPage(w, "my_rsvps", myRsvpsData{
			pageBase: pageBase{CSRFToken: csrfTokenFromRequest(r)},
		})
		return
	}

	sessToken := generateHighEntropyToken()
	if err := s.store.CreateMyRsvpsSession(sessToken, email, DefaultMyRsvpsSessionTTL); err != nil {
		// Storage failure is logged but does not surface. Render the
		// same page anyway (no enumeration) so the user can retry.
		log.Printf("web: create my-rsvps session for %s: %v", email, err)
	}

	// Build the absolute magic-link URL and send the email. The body
	// is content-free (no event names, no group names) per SECURITY.md
	// surface #3 and the anti-dox Check 7. The email is in the
	// recipient field only; it does NOT appear in the magic-link URL.
	magicLink := fmt.Sprintf("%s/my-rsvps?%s=%s", s.absoluteBaseURL(r), myRsvpsSessionKey, sessToken)
	subject, body, renderErr := emailpkg.RenderMyRsvpsLink(emailpkg.MyRsvpsLinkData{
		MagicLink:  magicLink,
		ExpiresIn:  "24 hours",
	})
	if renderErr != nil {
		log.Printf("web: render my-rsvps link email: %v", renderErr)
	} else if err := s.email.Send(r.Context(), email, subject, body); err != nil {
		log.Printf("web: send my-rsvps link email to %s: %v", email, err)
	}

	// ALWAYS render the same "check your email" page. Whether the
	// email is registered, the session was created, or the email was
	// sent successfully — the user sees the same response.
	s.renderPage(w, "my_rsvps", myRsvpsData{
		pageBase:   pageBase{CSRFToken: csrfTokenFromRequest(r)},
		LinkSentTo: email,
	})
}

// handleMyRsvpsLogout deletes the current session token (form value
// or query string) and re-renders the email-entry form. Idempotent.
func (s *Server) handleMyRsvpsLogout(w http.ResponseWriter, r *http.Request) {
	tok := r.FormValue(myRsvpsSessionKey)
	if tok == "" {
		tok = r.URL.Query().Get(myRsvpsSessionKey)
	}
	if tok != "" {
		_ = s.store.DeleteMyRsvpsSession(tok)
	}
	s.renderPage(w, "my_rsvps", myRsvpsData{
		pageBase: pageBase{CSRFToken: csrfTokenFromRequest(r)},
	})
}

// handleCancelRsvp cancels (deletes) the RSVP identified by the
// unguessable rsvp_token. The token is burned on use so a replay
// returns 404. Email + group_key + event_id is NOT a credential
// (see SECURITY.md, surface #2).
func (s *Server) handleCancelRsvp(w http.ResponseWriter, r *http.Request) {
	rsvpToken := r.FormValue("rsvp_token")
	if rsvpToken == "" {
		http.Error(w, "rsvp_token required", http.StatusBadRequest)
		return
	}

	rsvp, err := s.store.GetRsvpByToken(rsvpToken)
	if err != nil {
		http.Error(w, "rsvp not found", http.StatusNotFound)
		return
	}

	if err := s.store.CancelRsvp(rsvp.GroupKey, rsvp.EventID, rsvp.UserEmail); err != nil {
		log.Printf("web: cancel RSVP %s/%s/%s: %v", rsvp.GroupKey, rsvp.EventID, rsvp.UserEmail, err)
		http.Error(w, "failed to cancel", http.StatusInternalServerError)
		return
	}

	// Burn the token. A replay will hit GetRsvpByToken → 404.
	_ = s.store.DeleteRsvpByToken(rsvpToken)

	// Redirect to the email-entry form (NOT back to a list of someone
	// else's RSVPs; the email is not the credential anymore).
	http.Redirect(w, r, "/my-rsvps", http.StatusSeeOther)
}

// ---- RSVP magic-link flow ----

// handleRsvpConfirm renders the RSVP confirmation page when an attendee
// clicks the magic link.
func (s *Server) handleRsvpConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		s.renderNotFound(w, r)
		return
	}

	rsvp, err := s.store.GetRsvpByToken(token)
	if err != nil {
		s.renderNotFound(w, r)
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

	// Send RSVP-confirmed email to the attendee.
	eventTitle := "your event"
	eventLocation := ""
	var eventStartsAt int64
	if event, err := s.eventFromProduct(rsvp.GroupKey, rsvp.EventID); err == nil {
		eventTitle = event.Title
		eventLocation = event.Location
		eventStartsAt = event.StartsAt
	}
	groupName := rsvp.GroupKey
	if g, err := s.groupFromProduct(rsvp.GroupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	// Direct link to the RSVP management flow — the user types their
	// email there and gets a fresh magic link. The cancel URL is no
	// longer a per-user auto-login; the email is not the credential.
	cancelURL := fmt.Sprintf("%s/my-rsvps", s.absoluteBaseURL(r))

	subject, body, renderErr := emailpkg.RenderRsvpConfirmed(emailpkg.RsvpConfirmedData{
		EventTitle:    eventTitle,
		EventDate:     emailpkg.FormatEventDate(eventStartsAt),
		EventLocation: eventLocation,
		GroupName:     groupName,
		CancelURL:     cancelURL,
	})
	if renderErr != nil {
		log.Printf("web: render rsvp_confirmed email: %v", renderErr)
	} else if err := s.email.Send(context.Background(), rsvp.UserEmail, subject, body); err != nil {
		log.Printf("web: send rsvp_confirmed email to %s: %v", rsvp.UserEmail, err)
	}

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
	totalRsvps := 0
	var totalRevenueAmount uint64
	totalRevenueCurrency := "USD"

	for _, e := range events {
		count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
		totalRsvps += count

		// Get attendees from SQLite
		rsvpRecords, _ := s.store.ListRsvpsForEvent(e.GroupKey, e.EventID)
		var attendees []attendeeView
		checkedIn := 0
		for _, rec := range rsvpRecords {
			attendees = append(attendees, attendeeView{
				Email:    rec.UserEmail,
				Name:     rec.UserName,
				Attended: rec.Attended,
			})
			if rec.Attended {
				checkedIn++
			}
		}

		// Get capacity from product store
		capacity := e.Capacity
		if s.product != nil {
			if pe, ok := s.product.Store().GetEvent(e.EventID); ok {
				capacity = int(pe.Capacity)
			}
		}

		// Get ticket sales and revenue from product store
		ticketSold := 0
		var eventRevenueAmount uint64
		eventRevenueCurrency := "USD"
		if s.product != nil {
			orders := s.product.Store().OrdersForEvent(e.EventID)
			for _, o := range orders {
				if o.Status == pb.OrderStatus_ORDER_STATUS_COMPLETED || o.Status == pb.OrderStatus_ORDER_STATUS_PENDING {
					ticketSold++
					if o.AmountPaid != nil && o.Status == pb.OrderStatus_ORDER_STATUS_COMPLETED {
						eventRevenueAmount += o.AmountPaid.Amount
						if o.AmountPaid.Currency != "" {
							eventRevenueCurrency = o.AmountPaid.Currency
						}
						totalRevenueAmount += o.AmountPaid.Amount
						if o.AmountPaid.Currency != "" {
							totalRevenueCurrency = o.AmountPaid.Currency
						}
					}
				}
			}
		}

		dashEvents = append(dashEvents, dashboardEvent{
			EventID:       e.EventID,
			Title:         e.Title,
			StartsAt:      e.StartsAt,
			RsvpCount:     count,
			TicketSold:    ticketSold,
			EventRevenue:  &pb.Money{Amount: eventRevenueAmount, Currency: eventRevenueCurrency},
			Attendees:     attendees,
			Capacity:      capacity,
			CheckedIn:     checkedIn,
		})
	}

	s.renderPage(w, "dashboard", dashboardData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		GroupKey:     groupKey,
		Events:       dashEvents,
		TotalRevenue: &pb.Money{Amount: totalRevenueAmount, Currency: totalRevenueCurrency},
		TotalRsvps:   totalRsvps,
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

// ---- Group creation ----

// newGroupData is the template data for the group creation wizard.
type newGroupData struct {
	pageBase
	PrefillName     string
	PrefillDesc     string
	PrefillOrgName  string
	PrefillOrgEmail string
	Error           string
}

// handleNewGroup renders the group creation form.
func (s *Server) handleNewGroup(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "new_group", newGroupData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
	})
}

// handleCreateGroup processes the group creation form.
// Generates a 32-byte random group key and organizer token, creates the
// group in both the product store and SQLite cache, then creates an
// organizer session and redirects to the dashboard.
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	description := strings.TrimSpace(r.FormValue("description"))
	organizerName := strings.TrimSpace(r.FormValue("organizer_name"))
	organizerEmail := strings.TrimSpace(r.FormValue("organizer_email"))

	// Validate inputs
	if err := validateString("group name", displayName, 1, 256); err != nil {
		s.renderPage(w, "new_group", newGroupData{
			pageBase:       pageBase{CSRFToken: csrfTokenFromRequest(r)},
			PrefillName:    displayName, PrefillDesc: description,
			PrefillOrgName: organizerName, PrefillOrgEmail: organizerEmail,
			Error: err.Error(),
		})
		return
	}
	if err := validateString("description", description, 0, 4096); err != nil {
		s.renderPage(w, "new_group", newGroupData{
			pageBase:       pageBase{CSRFToken: csrfTokenFromRequest(r)},
			PrefillName:    displayName, PrefillDesc: description,
			PrefillOrgName: organizerName, PrefillOrgEmail: organizerEmail,
			Error: err.Error(),
		})
		return
	}
	if err := validateString("organizer name", organizerName, 1, 256); err != nil {
		s.renderPage(w, "new_group", newGroupData{
			pageBase:       pageBase{CSRFToken: csrfTokenFromRequest(r)},
			PrefillName:    displayName, PrefillDesc: description,
			PrefillOrgName: organizerName, PrefillOrgEmail: organizerEmail,
			Error: err.Error(),
		})
		return
	}
	if err := validateString("organizer email", organizerEmail, 3, 256); err != nil {
		s.renderPage(w, "new_group", newGroupData{
			pageBase:       pageBase{CSRFToken: csrfTokenFromRequest(r)},
			PrefillName:    displayName, PrefillDesc: description,
			PrefillOrgName: organizerName, PrefillOrgEmail: organizerEmail,
			Error: err.Error(),
		})
		return
	}

	// Generate 32-byte random group key (hex-encoded → 64-char hex string).
	groupKeyBytes := make([]byte, 32)
	if _, err := rand.Read(groupKeyBytes); err != nil {
		http.Error(w, "failed to generate group key", http.StatusInternalServerError)
		return
	}
	groupKey := hex.EncodeToString(groupKeyBytes)

	// Generate canonical name from display name (lowercase, hyphenated).
	canonicalName := canonicalizeName(displayName)
	// Ensure uniqueness by appending a short suffix of the group key.
	if len(groupKey) >= 8 {
		canonicalName = canonicalName + "-" + groupKey[:8]
	}

	// Generate organizer token (random 32-char hex).
	organizerToken := generateToken()

	// Create group in product store.
	if s.product != nil {
		group := &pb.Group{
			GroupId:       groupKey,
			CanonicalName: canonicalName,
			DisplayName:   displayName,
			Description:   description,
		}
		s.product.Store().PutGroup(group)
		s.product.Store().PutOrganizerToken(organizerToken, groupKey)
	}

	// Cache group in SQLite.
	_ = s.store.UpsertGroup(CachedGroup{
		GroupKey:      groupKey,
		CanonicalName: canonicalName,
		DisplayName:   displayName,
		Description:   description,
	})

	// Create organizer session.
	sessionToken := generateToken()
	if err := s.store.CreateSession(sessionToken, groupKey, 7*24*time.Hour); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, sessionToken)

	log.Printf("web: group created: %s (%s) — organizer: %s <%s>", groupKey, displayName, organizerName, organizerEmail)
	log.Printf("web: organizer token for %s: %s", groupKey, organizerToken)

	// Redirect to dashboard for the new group.
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// canonicalizeName converts a display name to a URL-safe canonical name.
// e.g. "Vegas Programmers!" → "vegas-programmers"
func canonicalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Replace spaces and non-alphanumeric chars with hyphens.
	var b strings.Builder
	prevHyphen := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		result = "group"
	}
	return result
}

// buildRRULE constructs an RFC 5545 RRULE string from the form parameters.
// recurrenceType: "daily", "weekly", "monthly"
// startsAt: the first occurrence start time (used for BYDAY when weekly)
// countStr: number of occurrences (optional, "0" or empty = no limit)
// endDate: end date in YYYY-MM-DD format (optional)
func buildRRULE(recurrenceType string, startsAt time.Time, countStr, endDate string) string {
	var freq string
	var byDay string

	switch strings.ToLower(recurrenceType) {
	case "daily":
		freq = "DAILY"
	case "weekly":
		freq = "WEEKLY"
		// Include BYDAY for the day of the week of the start time.
		byDay = weekdayToDayCode(startsAt.Weekday())
	case "monthly":
		freq = "MONTHLY"
	default:
		return ""
	}

	var parts []string
	parts = append(parts, "FREQ="+freq)
	if byDay != "" {
		parts = append(parts, "BYDAY="+byDay)
	}

	// COUNT or UNTIL limits
	count := 0
	if countStr != "" {
		count, _ = strconv.Atoi(countStr)
	}
	if count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(count))
	} else if endDate != "" {
		// Parse YYYY-MM-DD and format as YYYYMMDD for UNTIL.
		if t, err := time.Parse("2006-01-02", endDate); err == nil {
			parts = append(parts, "UNTIL="+t.Format("20060102"))
		}
	}

	return strings.Join(parts, ";")
}

// weekdayToDayCode converts a time.Weekday to the RFC 5545 two-letter code.
func weekdayToDayCode(wd time.Weekday) string {
	switch wd {
	case time.Sunday:
		return "SU"
	case time.Monday:
		return "MO"
	case time.Tuesday:
		return "TU"
	case time.Wednesday:
		return "WE"
	case time.Thursday:
		return "TH"
	case time.Friday:
		return "FR"
	case time.Saturday:
		return "SA"
	}
	return ""
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
	recurrenceType := r.FormValue("recurrence_type")
	recurrenceCountStr := r.FormValue("recurrence_count")
	recurrenceEndDate := r.FormValue("recurrence_until")

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

	// Build RRULE if a recurrence type was selected.
	var rrule string
	if recurrenceType != "" && recurrenceType != "none" {
		rrule = buildRRULE(recurrenceType, startsAt, recurrenceCountStr, recurrenceEndDate)
	}

	// Generate recurrence ID if we have a recurrence rule.
	recurrenceID := ""
	if rrule != "" {
		recurrenceID = generateToken()[:16]
	}

	// Generate the list of start times for recurring events.
	var startTimes []time.Time
	if rrule != "" {
		pattern, err := productrecurrence.ParseRRULE(rrule)
		if err != nil {
			s.renderFragment(w, "error_fragment", fragmentData{Error: "invalid recurrence rule: " + err.Error()})
			return
		}
		// Expand up to 2 years out or until the pattern's own limit.
		until := startsAt.AddDate(2, 0, 0)
		startTimes = productrecurrence.ExpandInstances(pattern, startsAt, until)
		if len(startTimes) == 0 {
			s.renderFragment(w, "error_fragment", fragmentData{Error: "recurrence produced no instances"})
			return
		}
	} else {
		startTimes = []time.Time{startsAt}
	}

	// Create events for each instance.
	var firstEventID string
	for i, st := range startTimes {
		eventID := generateToken()[:16]
		if i == 0 {
			firstEventID = eventID
		}

		// Store in SQLite cache
		ce := CachedEvent{
			GroupKey:    groupKey,
			EventID:     eventID,
			Title:       title,
			Description: description,
			StartsAt:    st.Unix(),
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
				StartsAt:    timestamppb.New(st),
				Location:    location,
				Capacity:    uint64(capacity),
				Paid:        false,
				Slug:        eventID,
			}
			if rrule != "" {
				event.Recurrence = &pb.RecurrenceRule{
					Rrule: rrule,
				}
			}
			s.product.Store().PutEvent(event)
		}
	}

	instanceCount := len(startTimes)
	log.Printf("web: event created: %s/%s — %s (%d instance(s)%s)", groupKey, firstEventID, title, instanceCount, func() string {
		if recurrenceID != "" {
			return ", recurrence_id=" + recurrenceID
		}
		return ""
	}())

	msg := fmt.Sprintf("Event created! (%d instances)", instanceCount)
	if rrule != "" {
		msg = fmt.Sprintf("Recurring event created! %d instances generated.", instanceCount)
	}
	_ = msg

	// Deliver the first event via ActivityPub if configured.
	if s.ap != nil {
		group, gok := s.product.Store().GetGroup(groupKey)
		groupName := groupKey
		if gok && group.CanonicalName != "" {
			groupName = group.CanonicalName
		}
		// Deliver the first instance via ActivityPub.
		// For recurring events, each instance has a unique ID generated
		// in the loop above, but we only have firstEventID for the first.
		// v0: deliver only the first instance.
		if len(startTimes) > 0 {
			event := &pb.Event{
				EventId:     firstEventID,
				GroupId:     groupKey,
				Title:       title,
				Description: description,
				StartsAt:    timestamppb.New(startTimes[0]),
				Location:    location,
				Capacity:    uint64(capacity),
			}
			report, err := s.ap.DeliverNewEvent(event, groupName)
			if err != nil {
				log.Printf("web: AP delivery for event %s failed: %v", firstEventID, err)
			} else if report != nil {
				log.Printf("web: AP delivery for event %s: %d success, %d fail", firstEventID, report.Successes, report.Failures)
			}
		}
	}

	s.renderFragment(w, "event_created_fragment", fragmentData{
		Title:    title,
		GroupKey: groupKey,
		EventID:  firstEventID,
	})
}

// handleCreateTicket creates a new ticket type for an event.
func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	eventID := r.PathValue("event_id")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	ticketName := r.FormValue("ticket_name")
	priceStr := r.FormValue("price_cents")
	capacityStr := r.FormValue("ticket_capacity")

	if err := validateString("ticket_name", ticketName, 1, 256); err != nil {
		s.renderFragment(w, "error_fragment", fragmentData{Error: err.Error()})
		return
	}

	price, err := strconv.ParseUint(priceStr, 10, 64)
	if err != nil {
		price = 0
	}
	capacity, err := strconv.ParseUint(capacityStr, 10, 64)
	if err != nil {
		capacity = 0
	}

	ticketID := "tick-" + generateToken()[:12]

	ticket := &pb.Ticket{
		TicketId: ticketID,
		Name:     ticketName,
		Price:    &pb.Money{Amount: price, Currency: "USD"},
		Capacity: capacity,
		Sold:     0,
	}

	if s.product != nil {
		s.product.Store().PutTicket(eventID, ticket)
	}

	log.Printf("web: ticket created: %s for event %s (group %s)", ticketID, eventID, groupKey)
	// Redirect back to dashboard
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// handleCheckIn marks an attendee as checked in for an event.
func (s *Server) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	_, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	// The route pattern uses {group_key} but we need event_id from the form
	eventID := r.FormValue("event_id")
	email := r.FormValue("email")

	if eventID == "" || email == "" {
		http.Error(w, "event_id and email required", http.StatusBadRequest)
		return
	}

	// Update check-in status in the product store
	if s.product != nil {
		if rsvp, ok := s.product.Store().GetRsvp(eventID, email); ok {
			rsvp.Attended = true
			s.product.Store().UpdateRsvp(rsvp)
		}
	}

	// Also update in SQLite
	_ = s.store.MarkRsvpAttended(eventID, email)

	log.Printf("web: attendee checked in: %s for event %s", email, eventID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ---- Checkout ----

// handleCheckout shows the order details and redirects to Stripe (or mock checkout).
func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("order_id")
	if orderID == "" {
		http.Error(w, "order_id required", http.StatusBadRequest)
		return
	}

	// Try local store first
	order, err := s.store.GetOrder(orderID)

	// Try product store if not found locally
	if err != nil && s.product != nil {
		if prodOrder, ok := s.product.Store().GetOrder(orderID); ok {
			var amount int
			if prodOrder.AmountPaid != nil {
				amount = int(prodOrder.AmountPaid.Amount)
			}
			order = OrderRecord{
				OrderID:         prodOrder.OrderId,
				GroupKey:        "",
				EventID:         "",
				Email:           prodOrder.AttendeeEmail,
				AmountCents:     amount,
				Status:          prodOrder.Status.String(),
				StripeSessionID: prodOrder.StripeSessionId,
			}
			_ = s.store.CreateOrder(order)
		}
	}

	if order.OrderID == "" {
		s.renderNotFound(w, r)
		return
	}

	// Get event title
	eventTitle := "Event"
	if order.EventID != "" {
		if event, err := s.eventFromProduct(order.GroupKey, order.EventID); err == nil {
			eventTitle = event.Title
		}
	}

	// Build checkout URL
	var checkoutURL string
	if order.StripeSessionID != "" {
		checkoutURL = fmt.Sprintf("https://checkout.stripe.com/c/pay/%s", order.StripeSessionID)
	}

	s.renderPage(w, "checkout", checkoutData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		Order:       order,
		EventTitle:  eventTitle,
		CheckoutURL: checkoutURL,
	})
}

// ---- Utility ----

// hexEncode is a convenience function.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

// handleEventICS generates an iCalendar (.ics) file for a single event.
// This lets users add the event to their calendar app (Google Calendar,
// Apple Calendar, Outlook) with one click.
func (s *Server) handleEventICS(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	eventID := r.PathValue("event_id")
	if groupKey == "" || eventID == "" {
		http.Error(w, "group_key and event_id required", http.StatusBadRequest)
		return
	}

	event, err := s.eventFromProduct(groupKey, eventID)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	start := time.Unix(event.StartsAt, 0).UTC()
	// Default duration: 2 hours if no end time specified.
	end := start.Add(2 * time.Hour)

	// Format: DTSTART:20260715T190000Z
	dtStart := start.Format("20060102T150405Z")
	dtEnd := end.Format("20060102T150405Z")
	dtStamp := time.Now().UTC().Format("20060102T150405Z")

	// Escape text values per RFC 5545.
	summary := icsEscape(event.Title)
	desc := icsEscape(event.Description)
	loc := icsEscape(event.Location)

	ics := "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//Federated Meetup//Event//EN\r\n" +
		"CALSCALE:GREGORIAN\r\n" +
		"METHOD:PUBLISH\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:" + eventID + "@federated-meetup\r\n" +
		"DTSTAMP:" + dtStamp + "\r\n" +
		"DTSTART:" + dtStart + "\r\n" +
		"DTEND:" + dtEnd + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DESCRIPTION:" + desc + "\r\n" +
		"LOCATION:" + loc + "\r\n" +
		"URL:" + baseURL(r) + "/events/" + groupKey + "/" + eventID + "\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+eventID+".ics")
	w.Write([]byte(ics))
}

// icsEscape escapes special characters for iCalendar text values per RFC 5545.
func icsEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// handleCancelEvent allows an organizer to cancel an event from the dashboard.
func (s *Server) handleCancelEvent(w http.ResponseWriter, r *http.Request) {
	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	eventID := r.PathValue("event_id")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	// Mark as cancelled in product store
	if s.product != nil {
		if e, ok := s.product.Store().GetEvent(eventID); ok {
			e.Cancelled = true
			s.product.Store().PutEvent(e)
		}
	}

	// Also update SQLite cache
	if ce, err := s.store.GetEvent(groupKey, eventID); err == nil {
		ce.Cancelled = true
		_ = s.store.UpsertEvent(ce)
	}

	log.Printf("web: event cancelled: %s/%s", groupKey, eventID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ---- Group ICS calendar subscription ----

// handleGroupICS generates an iCalendar (.ics) feed for all upcoming events
// in a group. This lets users subscribe to a group's calendar in their
// calendar app (Google Calendar, Apple Calendar, Outlook).
func (s *Server) handleGroupICS(w http.ResponseWriter, r *http.Request) {
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

	events, _ := s.store.ListUpcomingEvents(group.GroupKey, 100)

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

	now := s.now().Unix()
	var vevents []string
	for _, e := range events {
		if e.StartsAt < now || e.Cancelled {
			continue
		}
		start := time.Unix(e.StartsAt, 0).UTC()
		end := start.Add(2 * time.Hour)
		dtStart := start.Format("20060102T150405Z")
		dtEnd := end.Format("20060102T150405Z")
		dtStamp := time.Now().UTC().Format("20060102T150405Z")

		vevents = append(vevents,
			"BEGIN:VEVENT\r\n"+
				"UID:"+e.EventID+"@federated-meetup\r\n"+
				"DTSTAMP:"+dtStamp+"\r\n"+
				"DTSTART:"+dtStart+"\r\n"+
				"DTEND:"+dtEnd+"\r\n"+
				"SUMMARY:"+icsEscape(e.Title)+"\r\n"+
				"DESCRIPTION:"+icsEscape(e.Description)+"\r\n"+
				"LOCATION:"+icsEscape(e.Location)+"\r\n"+
				"URL:"+baseURL(r)+"/events/"+e.GroupKey+"/"+e.EventID+"\r\n"+
				"END:VEVENT\r\n")
	}

	ics := "BEGIN:VCALENDAR\r\n"+
		"VERSION:2.0\r\n"+
		"PRODID:-//Federated Meetup//Group Calendar//EN\r\n"+
		"CALSCALE:GREGORIAN\r\n"+
		"METHOD:PUBLISH\r\n"+
		"X-WR-CALNAME:"+icsEscape(group.DisplayName)+"\r\n"+
		"X-WR-CALDESC:"+icsEscape(group.Description)+"\r\n"+
		strings.Join(vevents, "")+
		"END:VCALENDAR\r\n"

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+group.CanonicalName+".ics")
	w.Write([]byte(ics))
}

// ---- Group RSS feed ----

// handleGroupRSS generates an Atom 1.0 feed for a group's upcoming events.
func (s *Server) handleGroupRSS(w http.ResponseWriter, r *http.Request) {
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

	now := s.now().Unix()
	base := baseURL(r)
	feedURL := base + "/groups/" + group.CanonicalName + "/feed.xml"

	var entries []string
	for _, e := range events {
		if e.StartsAt < now || e.Cancelled {
			continue
		}
		eventURL := base + "/events/" + e.GroupKey + "/" + e.EventID
		updated := time.Unix(e.StartsAt, 0).UTC().Format("2006-01-02T15:04:05Z")
		summary := xmlEscape(e.Title)
		desc := xmlEscape(e.Description)
		if desc == "" {
			desc = summary
		}
		entries = append(entries,
			"<entry>"+
				"<title>"+summary+"</title>"+
				"<link href=\""+eventURL+"\"/>"+
				"<id>"+eventURL+"</id>"+
				"<updated>"+updated+"</updated>"+
				"<summary>"+desc+"</summary>"+
				"</entry>")
	}

	updated := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	feed := "<?xml version=\"1.0\" encoding=\"utf-8\"?>"+
		"<feed xmlns=\"http://www.w3.org/2005/Atom\">"+
		"<title>"+xmlEscape(group.DisplayName)+" — Events</title>"+
		"<link href=\""+feedURL+"\" rel=\"self\"/>"+
		"<link href=\""+base+"/groups/"+group.CanonicalName+"\"/>"+
		"<id>"+feedURL+"</id>"+
		"<updated>"+updated+"</updated>"+
		strings.Join(entries, "")+
		"</feed>"

	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Write([]byte(feed))
}

// xmlEscape escapes special XML characters.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// ---- Attendee CSV export ----

// handleAttendeesCSV exports the attendee list for an event as CSV.
// Used by organizers for check-in sheets and record-keeping.
func (s *Server) handleAttendeesCSV(w http.ResponseWriter, r *http.Request) {
	groupKey, ok := s.isAuthenticated(r)
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}

	eventID := r.PathValue("event_id")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	rsvpRecords, _ := s.store.ListRsvpsForEvent(groupKey, eventID)

	// Get event title
	eventTitle := eventID
	if event, err := s.eventFromProduct(groupKey, eventID); err == nil {
		eventTitle = event.Title
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=attendees-"+eventID+".csv")

	// Write CSV
	w.Write([]byte("Name,Email,Status,Checked In\n"))
	for _, rec := range rsvpRecords {
		status := "Pending"
		if rec.Confirmed {
			status = "Confirmed"
		}
		checkedIn := "No"
		if rec.Attended {
			checkedIn = "Yes"
		}
		// CSV escape: wrap in quotes, double internal quotes
		name := csvEscape(rec.UserName)
		email := csvEscape(rec.UserEmail)
		w.Write([]byte(name + "," + email + "," + status + "," + checkedIn + "\n"))
	}

	log.Printf("web: CSV export: %d attendees for event %s (%s)", len(rsvpRecords), eventID, eventTitle)
}

// csvEscape escapes a string for CSV output.
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}