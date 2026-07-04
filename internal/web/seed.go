// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"log"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// SeedData populates the product store and SQLite cache with demo data
// if the store is empty. It is idempotent — calling it when data already
// exists is a no-op.
func (s *Server) SeedData() {
	if s.product == nil {
		return
	}
	ps := s.product.Store()

	// Check if data already exists — if the demo group exists, skip seeding.
	if _, ok := ps.GetGroup("vegas-programmers"); ok {
		return
	}

	now := s.now()

	group := &pb.Group{
		GroupId:       "vegas-programmers",
		CanonicalName: "vegas-programmers",
		DisplayName:   "Vegas Programmers",
		Description:   "A community of developers in Las Vegas building the future of software. We host talks, workshops, and hackathons covering Go, Rust, distributed systems, and more.",
	}
	ps.PutGroup(group)

	// Seed group in SQLite cache
	_ = s.store.UpsertGroup(CachedGroup{
		GroupKey:      group.GroupId,
		CanonicalName: group.CanonicalName,
		DisplayName:   group.DisplayName,
		Description:   group.Description,
	})

	// Also create an organizer token for the demo group
	ps.PutOrganizerToken("demo-organizer-token", group.GroupId)

	type seedEvent struct {
		eventID  string
		title    string
		desc     string
		loc      string
		capacity uint64
		paid     bool
		offset   int64 // days from now
		slug     string
		ticket   *pb.Ticket
	}

	events := []seedEvent{
		{
			eventID:  "evt-go-night",
			title:    "Go Night: Building Federated Systems",
			desc:     "A hands-on workshop where we build a federated meetup system in Go from scratch. We'll cover protocol design, ConnectRPC, SQLite, and deployment. Pizza and drinks provided. Bring your laptop with Go 1.25+ installed.",
			loc:      "Innovation Center, Downtown Las Vegas",
			capacity: 50,
			paid:     true,
			offset:   14,
			slug:     "go-night-federated-systems",
			ticket: &pb.Ticket{
				TicketId: "tick-go-night",
				Name:     "General Admission",
				Price:    &pb.Money{Amount: 2500, Currency: "USD"},
				Capacity: 50,
				Sold:     0,
			},
		},
		{
			eventID:  "evt-rust-vs-go",
			title:    "Rust vs Go Showdown",
			desc:     "Two developers. Two languages. One problem. Watch as we solve the same distributed systems challenge in Rust and Go, then debate the tradeoffs. Audience Q&A and networking after.",
			loc:      "Springs Preserve, Las Vegas",
			capacity: 100,
			paid:     false,
			offset:   7,
			slug:     "rust-vs-go-showdown",
		},
		{
			eventID:  "evt-hackathon",
			title:    "Hackathon: Build Something in 6 Hours",
			desc:     "Form teams of 2-4 and build a working prototype in 6 hours. Theme: Developer tools. Prizes for best project, most creative, and best use of AI. Coffee, snacks, and energy drinks provided throughout.",
			loc:      "UNLV Engineering Building",
			capacity: 30,
			paid:     true,
			offset:   21,
			slug:     "hackathon-6-hours",
			ticket: &pb.Ticket{
				TicketId: "tick-hackathon",
				Name:     "Participant Ticket",
				Price:    &pb.Money{Amount: 1000, Currency: "USD"},
				Capacity: 30,
				Sold:     0,
			},
		},
	}

	for _, se := range events {
		eventStart := now.AddDate(0, 0, int(se.offset))

		event := &pb.Event{
			EventId:     se.eventID,
			GroupId:     group.GroupId,
			Title:       se.title,
			Description: se.desc,
			StartsAt:    timestamppb.New(eventStart),
			Location:    se.loc,
			Capacity:    se.capacity,
			Paid:        se.paid,
			Slug:        se.slug,
		}
		ps.PutEvent(event)

		// Seed event in SQLite cache
		ce := CachedEvent{
			GroupKey:    group.GroupId,
			EventID:     se.eventID,
			Title:       se.title,
			Description: se.desc,
			StartsAt:    eventStart.Unix(),
			Location:    se.loc,
			Capacity:    int(se.capacity),
			Cancelled:   false,
		}
		_ = s.store.UpsertEvent(ce)

		if se.ticket != nil {
			ps.PutTicket(se.eventID, se.ticket)
		}
	}

	log.Printf("web: seeded demo data — group %q with %d events", group.DisplayName, len(events))
}