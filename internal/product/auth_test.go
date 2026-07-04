// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// assertConnectCode checks that err is a *connect.Error with the expected code.
func assertConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != want {
		t.Fatalf("expected code %s, got %s: %v", want, ce.Code(), ce)
	}
}

// --- No token → CodeUnauthenticated ---

func TestAuth_NoTokenReturnsUnauthenticated(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Seed an event so event_id lookups don't fail before the token check.
	seedEvent(store, "evt-auth", "grp-auth", "auth-event")

	t.Run("GetOrganizerDashboard", func(t *testing.T) {
		_, err := svc.GetOrganizerDashboard(ctx, connectReq(&pb.GetOrganizerDashboardRequest{
			GroupId: "grp-auth",
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("ListAttendees", func(t *testing.T) {
		_, err := svc.ListAttendees(ctx, connectReq(&pb.ListAttendeesRequest{
			EventId: "evt-auth",
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("CreateTicket", func(t *testing.T) {
		_, err := svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
			EventId: "evt-auth",
			Ticket: &pb.Ticket{
				Name: "Test",
				Price: &pb.Money{
					Amount:   1000,
					Currency: "USD",
				},
				Capacity: 10,
			},
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("RefundOrder", func(t *testing.T) {
		_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
			OrderId: "some-order-id",
			Amount:  1000,
			Reason:  "test",
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("ListOrders", func(t *testing.T) {
		_, err := svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
			EventId: "evt-auth",
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("CheckInAttendee", func(t *testing.T) {
		_, err := svc.CheckInAttendee(ctx, connectReq(&pb.CheckInAttendeeRequest{
			EventId:       "evt-auth",
			AttendeeEmail: "someone@example.com",
			// no OrganizerToken
		}))
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	})
}

// --- Cross-group token → CodePermissionDenied ---

func TestAuth_CrossGroupTokenReturnsPermissionDenied(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Two groups, each with an event.
	seedEvent(store, "evt-a", "grp-a", "event-a")
	seedEvent(store, "evt-b", "grp-b", "event-b")

	// Seed a ticket in grp-a so we can create an order for the RefundOrder test.
	seedTicket(store, "evt-a", "tkt-a", "Regular", 100, 5000)

	// Seed organizer token for grp-a only.
	seedOrganizerToken(store, "grp-a")

	// Also seed a separate token for grp-b (used for the RefundOrder cross-group test).
	const grpBToken = "grp-b-organizer-token"
	store.PutOrganizerToken(grpBToken, "grp-b")

	// Create a valid order in grp-a by purchasing a ticket.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt-a",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket setup failed: %v", err)
	}
	orderIDA := purchaseResp.Msg.OrderId

	t.Run("GetOrganizerDashboard", func(t *testing.T) {
		// testOrganizerToken is valid for grp-a; accessing grp-b should fail.
		_, err := svc.GetOrganizerDashboard(ctx, connectReq(&pb.GetOrganizerDashboardRequest{
			GroupId:        "grp-b",
			OrganizerToken: testOrganizerToken,
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ListAttendees", func(t *testing.T) {
		_, err := svc.ListAttendees(ctx, connectReq(&pb.ListAttendeesRequest{
			EventId:        "evt-b",
			OrganizerToken: testOrganizerToken,
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("CreateTicket", func(t *testing.T) {
		_, err := svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
			EventId:        "evt-b",
			OrganizerToken: testOrganizerToken,
			Ticket: &pb.Ticket{
				Name: "Cross Group",
				Price: &pb.Money{
					Amount:   2000,
					Currency: "USD",
				},
				Capacity: 50,
			},
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("RefundOrder", func(t *testing.T) {
		// Order belongs to grp-a; try refunding with grp-b's token.
		_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
			OrderId:        orderIDA,
			OrganizerToken: grpBToken,
			Amount:         5000,
			Reason:         "cross-group attempt",
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ListOrders", func(t *testing.T) {
		_, err := svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
			EventId:        "evt-b",
			OrganizerToken: testOrganizerToken,
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("CheckInAttendee", func(t *testing.T) {
		_, err := svc.CheckInAttendee(ctx, connectReq(&pb.CheckInAttendeeRequest{
			EventId:        "evt-b",
			AttendeeEmail:  "someone@example.com",
			OrganizerToken: testOrganizerToken,
		}))
		assertConnectCode(t, err, connect.CodePermissionDenied)
	})
}

// --- Valid token for correct group succeeds ---

func TestAuth_ValidTokenSucceeds(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt-ok", "grp-ok", "event-ok")
	seedOrganizerToken(store, "grp-ok")
	seedTicket(store, "evt-ok", "tkt-ok", "General", 100, 5000)

	// Submit an RSVP so CheckInAttendee has a target.
	_, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt-ok",
		Email:   "alice@example.com",
		Name:    "Alice",
	}))
	if err != nil {
		t.Fatalf("SubmitRsvp setup failed: %v", err)
	}

	// Purchase a ticket so RefundOrder has a target.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt-ok",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket setup failed: %v", err)
	}
	orderID := purchaseResp.Msg.OrderId

	t.Run("GetOrganizerDashboard", func(t *testing.T) {
		resp, err := svc.GetOrganizerDashboard(ctx, connectReq(&pb.GetOrganizerDashboardRequest{
			GroupId:        "grp-ok",
			OrganizerToken: testOrganizerToken,
		}))
		if err != nil {
			t.Fatalf("GetOrganizerDashboard failed: %v", err)
		}
		if resp == nil || resp.Msg == nil {
			t.Fatal("expected non-nil response")
		}
	})

	t.Run("ListAttendees", func(t *testing.T) {
		resp, err := svc.ListAttendees(ctx, connectReq(&pb.ListAttendeesRequest{
			EventId:        "evt-ok",
			OrganizerToken: testOrganizerToken,
		}))
		if err != nil {
			t.Fatalf("ListAttendees failed: %v", err)
		}
		if resp == nil || resp.Msg == nil {
			t.Fatal("expected non-nil response")
		}
	})

	t.Run("CreateTicket", func(t *testing.T) {
		resp, err := svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
			EventId:        "evt-ok",
			OrganizerToken: testOrganizerToken,
			Ticket: &pb.Ticket{
				Name: "VIP",
				Price: &pb.Money{
					Amount:   10000,
					Currency: "USD",
				},
				Capacity: 10,
			},
		}))
		if err != nil {
			t.Fatalf("CreateTicket failed: %v", err)
		}
		if resp.Msg.TicketId == "" {
			t.Fatal("expected non-empty ticket_id")
		}
	})

	t.Run("RefundOrder", func(t *testing.T) {
		resp, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
			OrderId:        orderID,
			OrganizerToken: testOrganizerToken,
			Amount:         5000,
			Reason:         "valid refund",
		}))
		if err != nil {
			t.Fatalf("RefundOrder failed: %v", err)
		}
		if resp == nil || resp.Msg == nil || resp.Msg.Order == nil {
			t.Fatal("expected non-nil response with order")
		}
		if resp.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
			t.Fatalf("expected REFUNDED status, got %s", resp.Msg.Order.Status)
		}
	})

	t.Run("ListOrders", func(t *testing.T) {
		resp, err := svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
			EventId:        "evt-ok",
			OrganizerToken: testOrganizerToken,
		}))
		if err != nil {
			t.Fatalf("ListOrders failed: %v", err)
		}
		if resp == nil || resp.Msg == nil {
			t.Fatal("expected non-nil response")
		}
	})

	t.Run("CheckInAttendee", func(t *testing.T) {
		resp, err := svc.CheckInAttendee(ctx, connectReq(&pb.CheckInAttendeeRequest{
			EventId:        "evt-ok",
			AttendeeEmail:  "alice@example.com",
			OrganizerToken: testOrganizerToken,
		}))
		if err != nil {
			t.Fatalf("CheckInAttendee failed: %v", err)
		}
		if !resp.Msg.CheckedIn {
			t.Fatal("expected checked_in=true")
		}
	})
}