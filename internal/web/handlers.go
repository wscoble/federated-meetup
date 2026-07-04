// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ---- Public pages ----

// handleHome renders the home page: list of groups + upcoming events.
// Supports ?q= search query to filter groups and events by name.
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

	query := strings.TrimSpace(r.URL.Query().Get("q"))
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
	if query == "" {
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
			if diff < 24*3600 {
				today = append(today, e)
			} else if diff < 7*24*3600 {
				thisWeek = append(thisWeek, e)
			} else {
				later = append(later, e)
			}
		}
	}

	s.renderPage(w, "home", homeData{
		Groups: groups, Events: events,
		TodayEvents: today, WeekEvents: thisWeek, LaterEvents: later,
		Query: query,
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

	// Split into upcoming and past.
	now := s.now().Unix()
	var upcoming, past []CachedEvent
	for _, e := range events {
		if e.StartsAt < now {
			past = append(past, e)
		} else {
			upcoming = append(upcoming, e)
		}
	}

	s.renderPage(w, "group", groupData{
		Group:          group,
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
		http.NotFound(w, r)
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

	// Stub email: log the magic link
	magicLink := fmt.Sprintf("/rsvp/%s", token)
	log.Printf("web: RSVP magic link for %s: %s (event %s/%s)", email, magicLink, groupKey, eventID)

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

// ---- My RSVPs ----

// handleMyRsvps shows all RSVPs for the current user (by email query param).
func (s *Server) handleMyRsvps(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))

	var rsvps []myRsvpView
	if email != "" {
		// Get RSVPs from SQLite store
		records, err := s.store.ListRsvpsByEmail(email)
		if err == nil {
			for _, rec := range records {
				eventTitle := "Unknown event"
				if event, err := s.eventFromProduct(rec.GroupKey, rec.EventID); err == nil {
					eventTitle = event.Title
				}
				rsvps = append(rsvps, myRsvpView{
					GroupKey:   rec.GroupKey,
					EventID:    rec.EventID,
					EventTitle: eventTitle,
					StartsAt:   0,
					Confirmed:  rec.Confirmed,
					Token:      rec.Token,
				})
			}
		}
	}

	s.renderPage(w, "my_rsvps", myRsvpsData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		Email: email,
		Rsvps: rsvps,
	})
}

// handleCancelRsvp cancels (deletes) a confirmed RSVP.
func (s *Server) handleCancelRsvp(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	groupKey := r.FormValue("group_key")
	eventID := r.FormValue("event_id")

	if email == "" || groupKey == "" || eventID == "" {
		http.Error(w, "email, group_key, and event_id required", http.StatusBadRequest)
		return
	}

	if err := s.store.CancelRsvp(groupKey, eventID, email); err != nil {
		log.Printf("web: cancel RSVP error: %v", err)
	}

	http.Redirect(w, r, "/my-rsvps?email="+email, http.StatusSeeOther)
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
	totalRsvps := 0
	var totalRevenueAmount uint64
	totalRevenueCurrency := "USD"

	for _, e := range events {
		count, _ := s.store.RsvpCount(e.GroupKey, e.EventID)
		totalRsvps += count

		// Get attendees from SQLite
		rsvpRecords, _ := s.store.ListRsvpsForEvent(e.GroupKey, e.EventID)
		var attendees []attendeeView
		for _, rec := range rsvpRecords {
			attendees = append(attendees, attendeeView{
				Email:    rec.UserEmail,
				Name:     rec.UserName,
				Attended: rec.Attended,
			})
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
			EventID:      e.EventID,
			Title:        e.Title,
			StartsAt:     e.StartsAt,
			RsvpCount:    count,
			TicketSold:   ticketSold,
			EventRevenue: &pb.Money{Amount: eventRevenueAmount, Currency: eventRevenueCurrency},
			Attendees:    attendees,
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
		http.NotFound(w, r)
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
		http.NotFound(w, r)
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