// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"sync"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
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
}

// NewStore creates a new empty Store.
func NewStore() *Store {
	return &Store{
		groups:       make(map[string]*pb.Group),
		events:        make(map[string]*pb.Event),
		tickets:       make(map[string]*pb.Ticket),
		ticketEvents:  make(map[string]string),
		orders:        make(map[string]*pb.Order),
		rsvps:         make(map[string]*pb.Rsvp),
		members:       make(map[string]*pb.GroupMember),
		tokens:        make(map[string]string),
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