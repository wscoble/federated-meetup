// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"sync"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Store is a thread-safe in-memory store for all product-layer domain objects.
// All maps are protected by a single sync.RWMutex.
//
// Ticket messages have no event_id field in the proto, so we maintain a
// separate ticketEvents map to track the ticket→event association.
type Store struct {
	mu sync.RWMutex

	groups       map[string]*pb.Group      // key: group_id
	events       map[string]*pb.Event       // key: event_id
	tickets      map[string]*pb.Ticket      // key: ticket_id
	ticketEvents map[string]string          // key: ticket_id → event_id
	orders       map[string]*pb.Order       // key: order_id
	rsvps        map[string]*pb.Rsvp        // key: event_id + ":" + email
	members      map[string]*pb.GroupMember // key: group_id + ":" + email
	tokens       map[string]string          // magic-link token → email
	organizerTokens map[string]string       // organizer token → group_id
}

// NewStore creates a new empty Store.
func NewStore() *Store {
	return &Store{
		groups:          make(map[string]*pb.Group),
		events:          make(map[string]*pb.Event),
		tickets:         make(map[string]*pb.Ticket),
		ticketEvents:    make(map[string]string),
		orders:          make(map[string]*pb.Order),
		rsvps:           make(map[string]*pb.Rsvp),
		members:         make(map[string]*pb.GroupMember),
		tokens:          make(map[string]string),
		organizerTokens: make(map[string]string),
	}
}

// --- Group ---

func (s *Store) PutGroup(g *pb.Group) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[g.GroupId] = g
}

func (s *Store) GetGroup(id string) (*pb.Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.groups[id]
	return g, ok
}

func (s *Store) DeleteGroup(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groups, id)
}

// --- Event ---

func (s *Store) PutEvent(e *pb.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[e.EventId] = e
}

func (s *Store) GetEvent(id string) (*pb.Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.events[id]
	return e, ok
}

func (s *Store) GetEventBySlug(slug string) (*pb.Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.events {
		if e.Slug == slug {
			return e, true
		}
	}
	return nil, false
}

func (s *Store) DeleteEvent(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, id)
}

// EventsForGroup returns all events for the given group_id.
func (s *Store) EventsForGroup(groupID string) []*pb.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*pb.Event
	for _, e := range s.events {
		if e.GroupId == groupID {
			out = append(out, e)
		}
	}
	return out
}

// --- Ticket ---

// PutTicket stores a ticket and associates it with the given eventID.
func (s *Store) PutTicket(eventID string, t *pb.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[t.TicketId] = t
	s.ticketEvents[t.TicketId] = eventID
}

func (s *Store) GetTicket(id string) (*pb.Ticket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tickets[id]
	return t, ok
}

// GetTicketEvent returns the event_id associated with the given ticket_id.
func (s *Store) GetTicketEvent(ticketID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eid, ok := s.ticketEvents[ticketID]
	return eid, ok
}

func (s *Store) UpdateTicket(t *pb.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[t.TicketId] = t
}

// TicketsForEvent returns all tickets belonging to the given event_id.
func (s *Store) TicketsForEvent(eventID string) []*pb.Ticket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*pb.Ticket
	for tid, eid := range s.ticketEvents {
		if eid == eventID {
			if t, ok := s.tickets[tid]; ok {
				out = append(out, t)
			}
		}
	}
	return out
}

func (s *Store) DeleteTicket(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tickets, id)
	delete(s.ticketEvents, id)
}

// --- Order ---

func (s *Store) PutOrder(o *pb.Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.OrderId] = o
}

func (s *Store) GetOrder(id string) (*pb.Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

func (s *Store) UpdateOrder(o *pb.Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.OrderId] = o
}

// OrdersForEvent returns all orders whose TicketId belongs to a ticket
// associated with the given event.
func (s *Store) OrdersForEvent(eventID string) []*pb.Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*pb.Order
	for _, o := range s.orders {
		if eid, ok := s.ticketEvents[o.TicketId]; ok && eid == eventID {
			out = append(out, o)
		}
	}
	return out
}

func (s *Store) DeleteOrder(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.orders, id)
}

// --- Rsvp ---

func rsvpKey(eventID, email string) string {
	return eventID + ":" + email
}

func (s *Store) PutRsvp(r *pb.Rsvp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rsvps[rsvpKey(r.EventId, r.UserEmail)] = r
}

func (s *Store) GetRsvp(eventID, email string) (*pb.Rsvp, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rsvps[rsvpKey(eventID, email)]
	return r, ok
}

func (s *Store) UpdateRsvp(r *pb.Rsvp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rsvps[rsvpKey(r.EventId, r.UserEmail)] = r
}

// RsvpsForEvent returns all RSVPs for the given event_id.
func (s *Store) RsvpsForEvent(eventID string) []*pb.Rsvp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*pb.Rsvp
	for _, r := range s.rsvps {
		if r.EventId == eventID {
			out = append(out, r)
		}
	}
	return out
}

// RsvpsForEmail returns all RSVPs for the given email.
func (s *Store) RsvpsForEmail(email string) []*pb.Rsvp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*pb.Rsvp
	for _, r := range s.rsvps {
		if r.UserEmail == email {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) DeleteRsvp(eventID, email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rsvps, rsvpKey(eventID, email))
}

// --- GroupMember ---

func memberKey(groupID, email string) string {
	return groupID + ":" + email
}

func (s *Store) PutMember(m *pb.GroupMember) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[memberKey(m.GroupId, m.UserEmail)] = m
}

func (s *Store) GetMember(groupID, email string) (*pb.GroupMember, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[memberKey(groupID, email)]
	return m, ok
}

func (s *Store) DeleteMember(groupID, email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members, memberKey(groupID, email))
}

// --- Token ---

func (s *Store) PutToken(token, email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = email
}

func (s *Store) GetTokenEmail(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	email, ok := s.tokens[token]
	return email, ok
}

func (s *Store) DeleteToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// --- Organizer Token ---

// PutOrganizerToken associates an organizer token with a group_id.
// This token is required for all organizer-scoped RPCs (CreateTicket,
// RefundOrder, ListOrders, etc.).
func (s *Store) PutOrganizerToken(token, groupID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.organizerTokens[token] = groupID
}

// GetOrganizerTokenGroup returns the group_id associated with an organizer
// token, or false if the token doesn't exist.
func (s *Store) GetOrganizerTokenGroup(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groupID, ok := s.organizerTokens[token]
	return groupID, ok
}

// ValidateOrganizerToken checks that the token exists and is scoped to the
// given group_id. Returns true if valid.
func (s *Store) ValidateOrganizerToken(token, groupID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gid, ok := s.organizerTokens[token]
	return ok && gid == groupID
}

// --- Atomic compound operations ---
//
// These methods perform check-and-mutate sequences under a single write lock
// to eliminate TOCTOU (time-of-check-to-time-of-use) races. Without them, a
// goroutine could read the ticket with an RLock, check capacity, release the
// lock, then try to write — and another goroutine could sneak in between the
// check and the write.

// AtomicPurchaseTicket checks capacity and, if sufficient, atomically
// increments the ticket's Sold counter and creates a pending Order. It returns
// the newly created order. If the ticket doesn't exist it returns (nil, false).
// If capacity is insufficient it returns (nil, true) with soldOut=true.
//
// The caller is responsible for providing the order fields (ticket_id,
// attendee_email, amount, currency, stripe_session_id); AtomicPurchaseTicket
// generates the order_id and sets status=PENDING and created_at.
func (s *Store) AtomicPurchaseTicket(
	ticketID, attendeeEmail, orderID, stripeSessionID string,
	quantity uint64,
	amount uint64,
	currency string,
) (*pb.Order, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ticket, ok := s.tickets[ticketID]
	if !ok {
		return nil, false, false // not found
	}

	// Check capacity (capacity==0 means unlimited).
	if ticket.Capacity > 0 && ticket.Sold+quantity > ticket.Capacity {
		return nil, true, true // found but sold out
	}

	// Atomically increment sold.
	ticket.Sold += quantity

	order := &pb.Order{
		OrderId:         orderID,
		TicketId:        ticketID,
		AttendeeEmail:   attendeeEmail,
		Status:          pb.OrderStatus_ORDER_STATUS_PENDING,
		StripeSessionId: stripeSessionID,
		AmountPaid: &pb.Money{
			Amount:   amount,
			Currency: currency,
		},
		CreatedAt: timestamppb.Now(),
	}
	s.orders[orderID] = order

	return order, true, false
}

// AtomicRefundOrder atomically processes a refund for an order. If amountCents
// is 0, it's a full refund: status → REFUNDED and sold count decremented.
// If amountCents > 0, it's a partial refund: status → PARTIALLY_REFUNDED,
// refunded_amount is incremented, and sold count is NOT decremented.
// Returns (order, found, alreadyRefunded):
//   - found=false if the order doesn't exist.
//   - alreadyRefunded=true if the order was already fully REFUNDED (no-op).
func (s *Store) AtomicRefundOrder(orderID string, amountCents uint64) (*pb.Order, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, ok := s.orders[orderID]
	if !ok {
		return nil, false, false
	}

	// Idempotency: don't process refunds on already-fully-refunded orders.
	if order.Status == pb.OrderStatus_ORDER_STATUS_REFUNDED {
		return order, true, true
	}

	// Initialize refunded_amount if nil.
	if order.RefundedAmount == nil {
		order.RefundedAmount = &pb.Money{Amount: 0, Currency: order.AmountPaid.Currency}
	}

	if amountCents == 0 {
		// Full refund.
		order.Status = pb.OrderStatus_ORDER_STATUS_REFUNDED
		order.RefundedAt = timestamppb.Now()
		order.RefundedAmount.Amount = order.AmountPaid.Amount

		// Decrement the associated ticket's sold count (but not below zero).
		if ticket, ok := s.tickets[order.TicketId]; ok {
			if ticket.Sold > 0 {
				ticket.Sold--
			}
		}
	} else {
		// Partial refund.
		order.RefundedAmount.Amount += amountCents
		order.RefundedAmount.Currency = order.AmountPaid.Currency

		// If cumulative refunds equal the full amount, transition to REFUNDED.
		if order.RefundedAmount.Amount >= order.AmountPaid.Amount {
			order.Status = pb.OrderStatus_ORDER_STATUS_REFUNDED
			order.RefundedAt = timestamppb.Now()

			// Decrement sold count since the ticket is effectively returned.
			if ticket, ok := s.tickets[order.TicketId]; ok {
				if ticket.Sold > 0 {
					ticket.Sold--
				}
			}
		} else {
			order.Status = pb.OrderStatus_ORDER_STATUS_PARTIALLY_REFUNDED
		}
	}

	return order, true, false
}

// AtomicCompleteOrder transitions an order from PENDING to COMPLETED.
// This is called when the Stripe webhook confirms a successful payment.
// Returns (order, found, alreadyCompleted):
//   - found=false if the order doesn't exist.
//   - alreadyCompleted=true if the order was already COMPLETED (idempotent).
func (s *Store) AtomicCompleteOrder(orderID string) (*pb.Order, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, ok := s.orders[orderID]
	if !ok {
		return nil, false, false
	}

	if order.Status == pb.OrderStatus_ORDER_STATUS_COMPLETED {
		return order, true, true
	}

	order.Status = pb.OrderStatus_ORDER_STATUS_COMPLETED
	return order, true, false
}

// AtomicMarkOrderFailed transitions an order from PENDING to FAILED.
// Called when a Stripe payment fails or is cancelled.
// Returns (order, found, alreadyTerminal):
//   - found=false if the order doesn't exist.
//   - alreadyTerminal=true if the order was already in a terminal state
//     (COMPLETED, REFUNDED, FAILED, DISPUTED).
func (s *Store) AtomicMarkOrderFailed(orderID string) (*pb.Order, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, ok := s.orders[orderID]
	if !ok {
		return nil, false, false
	}

	// Don't transition from a terminal state.
	if order.Status != pb.OrderStatus_ORDER_STATUS_PENDING {
		return order, true, true
	}

	order.Status = pb.OrderStatus_ORDER_STATUS_FAILED

	// Decrement sold count since the purchase didn't complete.
	if ticket, ok := s.tickets[order.TicketId]; ok {
		if ticket.Sold > 0 {
			ticket.Sold--
		}
	}

	return order, true, false
}

// AtomicMarkOrderDisputed transitions an order to DISPUTED.
// Called when a Stripe dispute webhook is received.
func (s *Store) AtomicMarkOrderDisputed(orderID string) (*pb.Order, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, ok := s.orders[orderID]
	if !ok {
		return nil, false, false
	}

	if order.Status == pb.OrderStatus_ORDER_STATUS_DISPUTED {
		return order, true, true
	}

	order.Status = pb.OrderStatus_ORDER_STATUS_DISPUTED
	return order, true, false
}