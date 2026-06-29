// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"connectrpc.com/connect"

	federatedmeetupproductv1connect "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1/federatedmeetupproductv1connect"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Compile-time assertion that Service satisfies the generated handler interface.
var _ federatedmeetupproductv1connect.ProductServiceHandler = (*Service)(nil)

// Service implements ProductServiceHandler backed by an in-memory Store.
type Service struct {
	store *Store
}

// NewService creates a Service backed by the given Store.
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// newID generates a random 16-char hex ID using crypto/rand.
func newID() string {
	b := make([]byte, 8) // 8 bytes = 16 hex chars
	if _, err := rand.Read(b); err != nil {
		// Fallback should never happen, but provide a safe default.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// newToken generates a random 32-char hex token for magic-links.
func newToken() string {
	b := make([]byte, 16) // 16 bytes = 32 hex chars
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// defaultPageSize clamps the page size to a reasonable default.
func defaultPageSize(sz uint32) uint32 {
	if sz == 0 || sz > 100 {
		return 25
	}
	return sz
}

// ---------------------------------------------------------------------------
// Public reads
// ---------------------------------------------------------------------------

// GetPublicEvent looks up an event by slug or event_id and returns the event,
// its tickets, the RSVP count, and the capacity. No auth required.
func (s *Service) GetPublicEvent(
	ctx context.Context,
	req *connect.Request[pb.GetPublicEventRequest],
) (*connect.Response[pb.GetPublicEventResponse], error) {
	var event *pb.Event
	if req.Msg.Slug != "" {
		e, ok := s.store.GetEventBySlug(req.Msg.Slug)
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("event not found"))
		}
		event = e
	} else if req.Msg.EventId != "" {
		e, ok := s.store.GetEvent(req.Msg.EventId)
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("event not found"))
		}
		event = e
	} else {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("must provide slug or event_id"))
	}

	tickets := s.store.TicketsForEvent(event.EventId)
	rsvps := s.store.RsvpsForEvent(event.EventId)
	var rsvpCount uint64
	for _, r := range rsvps {
		if r.Status == pb.RsvpStatus_RSVP_STATUS_GOING {
			rsvpCount++
		}
	}

	return connect.NewResponse(&pb.GetPublicEventResponse{
		Event:     event,
		Tickets:   tickets,
		RsvpCount: rsvpCount,
		Capacity:  event.Capacity,
	}), nil
}

// ListUpcomingEvents lists events for a group, paginated by cursor (event_id).
func (s *Service) ListUpcomingEvents(
	ctx context.Context,
	req *connect.Request[pb.ListUpcomingEventsRequest],
) (*connect.Response[pb.ListUpcomingEventsResponse], error) {
	pageSize := defaultPageSize(req.Msg.PageSize)
	events := s.store.EventsForGroup(req.Msg.GroupId)

	// Sort by event_id for stable pagination.
	sort.Slice(events, func(i, j int) bool {
		return events[i].EventId < events[j].EventId
	})

	// Filter out cancelled events.
	var active []*pb.Event
	for _, e := range events {
		if !e.Cancelled {
			active = append(active, e)
		}
	}
	events = active

	// Apply cursor.
	if req.Msg.Cursor != "" {
		var filtered []*pb.Event
		seen := false
		for _, e := range events {
			if seen {
				filtered = append(filtered, e)
			}
			if e.EventId == req.Msg.Cursor {
				seen = true
			}
		}
		events = filtered
	}

	// Truncate to page size.
	var nextCursor string
	if uint32(len(events)) > pageSize {
		events = events[:pageSize]
		if len(events) > 0 {
			nextCursor = events[len(events)-1].EventId
		}
	}

	return connect.NewResponse(&pb.ListUpcomingEventsResponse{
		Events:     events,
		NextCursor: nextCursor,
	}), nil
}

// ListTickets lists all tickets for an event.
func (s *Service) ListTickets(
	ctx context.Context,
	req *connect.Request[pb.ListTicketsRequest],
) (*connect.Response[pb.ListTicketsResponse], error) {
	if req.Msg.EventId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id is required"))
	}
	tickets := s.store.TicketsForEvent(req.Msg.EventId)
	return connect.NewResponse(&pb.ListTicketsResponse{
		Tickets: tickets,
	}), nil
}

// ---------------------------------------------------------------------------
// Attendee writes (magic-link auth)
// ---------------------------------------------------------------------------

// SubmitRsvp creates a RSVP for the given event + email, generates a magic-link
// token, and returns the RSVP and the magic link.
func (s *Service) SubmitRsvp(
	ctx context.Context,
	req *connect.Request[pb.SubmitRsvpRequest],
) (*connect.Response[pb.SubmitRsvpResponse], error) {
	if req.Msg.EventId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id is required"))
	}
	if req.Msg.Email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("email is required"))
	}

	// Verify event exists.
	if _, ok := s.store.GetEvent(req.Msg.EventId); !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("event not found"))
	}

	// Check for existing RSVP.
	if existing, ok := s.store.GetRsvp(req.Msg.EventId, req.Msg.Email); ok {
		// Return existing RSVP with a fresh magic-link token.
		token := newToken()
		s.store.PutToken(token, req.Msg.Email)
		magicLink := fmt.Sprintf("https://app.federatedmeetup.com/rsvp?token=%s", token)
		return connect.NewResponse(&pb.SubmitRsvpResponse{
			Rsvp:      existing,
			MagicLink: magicLink,
		}), nil
	}

	rsvp := &pb.Rsvp{
		EventId:   req.Msg.EventId,
		UserEmail: req.Msg.Email,
		Status:    pb.RsvpStatus_RSVP_STATUS_GOING,
		CreatedAt: timestamppb.Now(),
		Attended:  false,
	}
	s.store.PutRsvp(rsvp)

	token := newToken()
	s.store.PutToken(token, req.Msg.Email)
	magicLink := fmt.Sprintf("https://app.federatedmeetup.com/rsvp?token=%s", token)

	return connect.NewResponse(&pb.SubmitRsvpResponse{
		Rsvp:      rsvp,
		MagicLink: magicLink,
	}), nil
}

// CancelRsvp verifies the token and sets the RSVP status to NOT_GOING.
func (s *Service) CancelRsvp(
	ctx context.Context,
	req *connect.Request[pb.CancelRsvpRequest],
) (*connect.Response[pb.CancelRsvpResponse], error) {
	if req.Msg.EventId == "" || req.Msg.Email == "" || req.Msg.Token == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id, email, and token are required"))
	}

	// Verify token maps to this email.
	tokenEmail, ok := s.store.GetTokenEmail(req.Msg.Token)
	if !ok || tokenEmail != req.Msg.Email {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("invalid token"))
	}

	rsvp, ok := s.store.GetRsvp(req.Msg.EventId, req.Msg.Email)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("rsvp not found"))
	}

	rsvp.Status = pb.RsvpStatus_RSVP_STATUS_NOT_GOING
	s.store.UpdateRsvp(rsvp)

	return connect.NewResponse(&pb.CancelRsvpResponse{
		Cancelled: true,
	}), nil
}

// ListMyRsvps lists all RSVPs for an email (verify token matches the email).
func (s *Service) ListMyRsvps(
	ctx context.Context,
	req *connect.Request[pb.ListMyRsvpsRequest],
) (*connect.Response[pb.ListMyRsvpsResponse], error) {
	if req.Msg.Email == "" || req.Msg.Token == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("email and token are required"))
	}

	// Verify token maps to this email.
	tokenEmail, ok := s.store.GetTokenEmail(req.Msg.Token)
	if !ok || tokenEmail != req.Msg.Email {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("invalid token"))
	}

	rsvps := s.store.RsvpsForEmail(req.Msg.Email)
	return connect.NewResponse(&pb.ListMyRsvpsResponse{
		Rsvps: rsvps,
	}), nil
}

// ---------------------------------------------------------------------------
// Attendee purchases
// ---------------------------------------------------------------------------

// PurchaseTicket creates a mock Stripe checkout session, creates an Order with
// Pending status, and increments the ticket's sold count.
func (s *Service) PurchaseTicket(
	ctx context.Context,
	req *connect.Request[pb.PurchaseTicketRequest],
) (*connect.Response[pb.PurchaseTicketResponse], error) {
	if req.Msg.TicketId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("ticket_id is required"))
	}
	if req.Msg.AttendeeEmail == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("attendee_email is required"))
	}

	quantity := req.Msg.Quantity
	if quantity == 0 {
		quantity = 1
	}

	ticket, ok := s.store.GetTicket(req.Msg.TicketId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("ticket not found"))
	}

	// Check sold-out.
	if ticket.Capacity > 0 && ticket.Sold+uint64(quantity) > ticket.Capacity {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("ticket is sold out"))
	}

	orderID := newID()
	sessionID := newID()
	checkoutURL := fmt.Sprintf("https://checkout.stripe.com/c/pay/%s", sessionID)

	// Compute amount paid.
	var amount uint64
	if ticket.Price != nil {
		amount = ticket.Price.Amount * uint64(quantity)
	}
	currency := "USD"
	if ticket.Price != nil && ticket.Price.Currency != "" {
		currency = ticket.Price.Currency
	}

	order := &pb.Order{
		OrderId:         orderID,
		TicketId:        req.Msg.TicketId,
		AttendeeEmail:   req.Msg.AttendeeEmail,
		Status:          pb.OrderStatus_ORDER_STATUS_PENDING,
		StripeSessionId: sessionID,
		AmountPaid: &pb.Money{
			Amount:   amount,
			Currency: currency,
		},
		CreatedAt: timestamppb.Now(),
	}
	s.store.PutOrder(order)

	// Increment sold.
	ticket.Sold += uint64(quantity)
	s.store.UpdateTicket(ticket)

	return connect.NewResponse(&pb.PurchaseTicketResponse{
		OrderId:           orderID,
		StripeCheckoutUrl: checkoutURL,
	}), nil
}

// ---------------------------------------------------------------------------
// Organizer (organizer-token auth)
// ---------------------------------------------------------------------------

// GetOrganizerDashboard aggregates: upcoming events, total RSVPs, total revenue
// (sum of completed orders), and a pending actions list.
func (s *Service) GetOrganizerDashboard(
	ctx context.Context,
	req *connect.Request[pb.GetOrganizerDashboardRequest],
) (*connect.Response[pb.GetOrganizerDashboardResponse], error) {
	if req.Msg.GroupId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("group_id is required"))
	}

	events := s.store.EventsForGroup(req.Msg.GroupId)

	// Filter to non-cancelled events.
	var upcoming []*pb.Event
	for _, e := range events {
		if !e.Cancelled {
			upcoming = append(upcoming, e)
		}
	}

	// Total RSVPs across all events in this group.
	var totalRsvps uint64
	for _, e := range upcoming {
		rsvps := s.store.RsvpsForEvent(e.EventId)
		for _, r := range rsvps {
			if r.Status == pb.RsvpStatus_RSVP_STATUS_GOING {
				totalRsvps++
			}
		}
	}

	// Total revenue: sum of completed orders for tickets in events of this group.
	var totalRevenueAmount uint64
	totalRevenueCurrency := "USD"
	for _, e := range upcoming {
		orders := s.store.OrdersForEvent(e.EventId)
		for _, o := range orders {
			if o.Status == pb.OrderStatus_ORDER_STATUS_COMPLETED {
				if o.AmountPaid != nil {
					totalRevenueAmount += o.AmountPaid.Amount
					if o.AmountPaid.Currency != "" {
						totalRevenueCurrency = o.AmountPaid.Currency
					}
				}
			}
		}
	}

	// Pending actions: orders with pending status.
	var pendingActions []string
	for _, e := range upcoming {
		orders := s.store.OrdersForEvent(e.EventId)
		for _, o := range orders {
			if o.Status == pb.OrderStatus_ORDER_STATUS_PENDING {
				pendingActions = append(pendingActions,
					fmt.Sprintf("Order %s is pending for event %s", o.OrderId, e.EventId))
			}
		}
	}

	return connect.NewResponse(&pb.GetOrganizerDashboardResponse{
		UpcomingEvents: upcoming,
		TotalRsvps:     totalRsvps,
		TotalRevenue: &pb.Money{
			Amount:   totalRevenueAmount,
			Currency: totalRevenueCurrency,
		},
		PendingActions: pendingActions,
	}), nil
}

// ListAttendees lists RSVPs for an event with status Going.
func (s *Service) ListAttendees(
	ctx context.Context,
	req *connect.Request[pb.ListAttendeesRequest],
) (*connect.Response[pb.ListAttendeesResponse], error) {
	if req.Msg.EventId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id is required"))
	}

	rsvps := s.store.RsvpsForEvent(req.Msg.EventId)
	var attendees []*pb.Rsvp
	for _, r := range rsvps {
		if r.Status == pb.RsvpStatus_RSVP_STATUS_GOING {
			attendees = append(attendees, r)
		}
	}

	return connect.NewResponse(&pb.ListAttendeesResponse{
		Attendees: attendees,
	}), nil
}

// CreateTicket creates a new ticket for an event and returns the ticket_id.
func (s *Service) CreateTicket(
	ctx context.Context,
	req *connect.Request[pb.CreateTicketRequest],
) (*connect.Response[pb.CreateTicketResponse], error) {
	if req.Msg.EventId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id is required"))
	}
	if req.Msg.Ticket == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("ticket is required"))
	}

	// Verify event exists.
	if _, ok := s.store.GetEvent(req.Msg.EventId); !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("event not found"))
	}

	ticket := req.Msg.Ticket
	ticket.TicketId = newID()
	s.store.PutTicket(req.Msg.EventId, ticket)

	return connect.NewResponse(&pb.CreateTicketResponse{
		TicketId: ticket.TicketId,
	}), nil
}

// RefundOrder sets the order status to Refunded, sets refunded_at, and
// decrements the ticket's sold count.
func (s *Service) RefundOrder(
	ctx context.Context,
	req *connect.Request[pb.RefundOrderRequest],
) (*connect.Response[pb.RefundOrderResponse], error) {
	if req.Msg.OrderId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("order_id is required"))
	}

	order, ok := s.store.GetOrder(req.Msg.OrderId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("order not found"))
	}

	// Set status to refunded.
	order.Status = pb.OrderStatus_ORDER_STATUS_REFUNDED
	order.RefundedAt = timestamppb.Now()
	s.store.UpdateOrder(order)

	// Decrement ticket sold count.
	if ticket, ok := s.store.GetTicket(order.TicketId); ok {
		if ticket.Sold > 0 {
			ticket.Sold--
			s.store.UpdateTicket(ticket)
		}
	}

	return connect.NewResponse(&pb.RefundOrderResponse{
		Order: order,
	}), nil
}

// ListOrders lists orders for an event, paginated by cursor (order_id).
func (s *Service) ListOrders(
	ctx context.Context,
	req *connect.Request[pb.ListOrdersRequest],
) (*connect.Response[pb.ListOrdersResponse], error) {
	if req.Msg.EventId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id is required"))
	}

	pageSize := defaultPageSize(req.Msg.PageSize)
	orders := s.store.OrdersForEvent(req.Msg.EventId)

	// Sort by order_id for stable pagination.
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].OrderId < orders[j].OrderId
	})

	// Apply cursor.
	if req.Msg.Cursor != "" {
		var filtered []*pb.Order
		seen := false
		for _, o := range orders {
			if seen {
				filtered = append(filtered, o)
			}
			if o.OrderId == req.Msg.Cursor {
				seen = true
			}
		}
		orders = filtered
	}

	// Truncate to page size.
	var nextCursor string
	if uint32(len(orders)) > pageSize {
		orders = orders[:pageSize]
		if len(orders) > 0 {
			nextCursor = orders[len(orders)-1].OrderId
		}
	}

	return connect.NewResponse(&pb.ListOrdersResponse{
		Orders:     orders,
		NextCursor: nextCursor,
	}), nil
}

// CheckInAttendee marks a RSVP as attended=true.
func (s *Service) CheckInAttendee(
	ctx context.Context,
	req *connect.Request[pb.CheckInAttendeeRequest],
) (*connect.Response[pb.CheckInAttendeeResponse], error) {
	if req.Msg.EventId == "" || req.Msg.AttendeeEmail == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event_id and attendee_email are required"))
	}

	rsvp, ok := s.store.GetRsvp(req.Msg.EventId, req.Msg.AttendeeEmail)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("rsvp not found"))
	}

	rsvp.Attended = true
	s.store.UpdateRsvp(rsvp)

	return connect.NewResponse(&pb.CheckInAttendeeResponse{
		CheckedIn: true,
	}), nil
}

