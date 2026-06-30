// SPDX-License-Identifier: AGPL-3.0
//
// Integration test: Product service over real HTTP via ConnectRPC.
// Seeds a group + event + ticket, then exercises the full purchase
// flow through the ConnectRPC client → server boundary.

package product_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/product"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
	"github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1/federatedmeetupproductv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"connectrpc.com/connect"
)

func setupTestServer(t *testing.T) (federatedmeetupproductv1connect.ProductServiceClient, *product.Store) {
	store := product.NewStore()

	// Seed a group + event + ticket for testing
	store.PutGroup(&pb.Group{
		GroupId:       "grp-yoga",
		CanonicalName: "vegas-yoga",
		DisplayName:   "Vegas Yoga Collective",
		HostingMode:   pb.HostingMode_HOSTING_MODE_PAID_HOST,
		HostingTier:   pb.HostingTier_HOSTING_TIER_PRO,
		Package:       pb.Package_PACKAGE_TICKETED_WORKSHOP,
		CreatedAt:     timestamppb.Now(),
	})

	// Seed organizer token for the group so organizer RPCs work.
	store.PutOrganizerToken("test-organizer-token", "grp-yoga")

	store.PutEvent(&pb.Event{
		EventId:    "evt-flow-1",
		GroupId:    "grp-yoga",
		Title:      "Sunset Yoga Workshop",
		Slug:       "sunset-yoga-june",
		StartsAt:   timestamppb.New(time.Now().Add(24 * time.Hour)),
		EndsAt:     timestamppb.New(time.Now().Add(26 * time.Hour)),
		Location:   "Sunset Park, Henderson NV",
		Visibility: pb.EventVisibility_EVENT_VISIBILITY_PUBLIC,
		Capacity:   20,
		Package:    pb.Package_PACKAGE_TICKETED_WORKSHOP,
		Paid:       true,
	})

	store.PutTicket("evt-flow-1", &pb.Ticket{
		TicketId:      "tkt-regular",
		Name:          "Regular",
		TierType:      pb.TicketTierType_TICKET_TIER_TYPE_REGULAR,
		Price:         &pb.Money{Amount: 5000, Currency: "USD"},
		Capacity:      20,
		Sold:          0,
		SaleStartsAt:  timestamppb.New(time.Now().Add(-1 * time.Hour)),
		SaleEndsAt:    timestamppb.New(time.Now().Add(12 * time.Hour)),
	})

	svc := product.NewService(store, nil)
	mux := http.NewServeMux()
	path, handler := federatedmeetupproductv1connect.NewProductServiceHandler(svc)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := federatedmeetupproductv1connect.NewProductServiceClient(
		http.DefaultClient, server.URL,
	)
	return client, store
}

func TestIntegration_FullPurchaseFlow(t *testing.T) {
	ctx := context.Background()
	client, store := setupTestServer(t)

	// 1. Get public event by slug
	pubResp, err := client.GetPublicEvent(ctx, connect.NewRequest(&pb.GetPublicEventRequest{
		Slug: "sunset-yoga-june",
	}))
	if err != nil {
		t.Fatalf("GetPublicEvent: %v", err)
	}
	if pubResp.Msg.Event.Title != "Sunset Yoga Workshop" {
		t.Errorf("expected title 'Sunset Yoga Workshop', got %q", pubResp.Msg.Event.Title)
	}
	if len(pubResp.Msg.Tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(pubResp.Msg.Tickets))
	}
	if pubResp.Msg.Tickets[0].Price.Amount != 5000 {
		t.Errorf("expected price 5000, got %d", pubResp.Msg.Tickets[0].Price.Amount)
	}

	// 2. List tickets for the event
	listResp, err := client.ListTickets(ctx, connect.NewRequest(&pb.ListTicketsRequest{
		EventId: "evt-flow-1",
	}))
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(listResp.Msg.Tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(listResp.Msg.Tickets))
	}

	// 3. Purchase ticket
	purchaseResp, err := client.PurchaseTicket(ctx, connect.NewRequest(&pb.PurchaseTicketRequest{
		TicketId:      "tkt-regular",
		AttendeeEmail: "carol@example.com",
		AttendeeName:  "Carol",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket: %v", err)
	}
	if purchaseResp.Msg.OrderId == "" {
		t.Fatal("expected non-empty order_id")
	}
	if purchaseResp.Msg.StripeCheckoutUrl == "" {
		t.Fatal("expected non-empty stripe_checkout_url")
	}

	// 4. Verify sold count incremented
	tkt, ok := store.GetTicket("tkt-regular")
	if !ok {
		t.Fatal("ticket not found in store")
	}
	if tkt.Sold != 1 {
		t.Errorf("expected sold=1, got %d", tkt.Sold)
	}

	// 5. List orders for the event
	ordersResp, err := client.ListOrders(ctx, connect.NewRequest(&pb.ListOrdersRequest{
		EventId:        "evt-flow-1",
		OrganizerToken: "test-organizer-token",
	}))
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(ordersResp.Msg.Orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(ordersResp.Msg.Orders))
	}
	if ordersResp.Msg.Orders[0].Status != pb.OrderStatus_ORDER_STATUS_PENDING {
		t.Errorf("expected PENDING status, got %v", ordersResp.Msg.Orders[0].Status)
	}

	// 6. Refund the order
	refundResp, err := client.RefundOrder(ctx, connect.NewRequest(&pb.RefundOrderRequest{
		OrderId:        purchaseResp.Msg.OrderId,
		OrganizerToken: "test-organizer-token",
		Amount:         5000,
		Reason:         "customer requested",
	}))
	if err != nil {
		t.Fatalf("RefundOrder: %v", err)
	}
	if refundResp.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Errorf("expected REFUNDED status, got %v", refundResp.Msg.Order.Status)
	}

	// 7. Verify sold count decremented
	tkt, _ = store.GetTicket("tkt-regular")
	if tkt.Sold != 0 {
		t.Errorf("expected sold=0 after refund, got %d", tkt.Sold)
	}
}

func TestIntegration_RSVPFlow(t *testing.T) {
	ctx := context.Background()
	client, _ := setupTestServer(t)

	// 1. Submit RSVP
	rsvpResp, err := client.SubmitRsvp(ctx, connect.NewRequest(&pb.SubmitRsvpRequest{
		EventId: "evt-flow-1",
		Email:   "dave@example.com",
		Name:    "Dave",
	}))
	if err != nil {
		t.Fatalf("SubmitRsvp: %v", err)
	}
	if rsvpResp.Msg.Rsvp == nil {
		t.Fatal("expected non-nil rsvp")
	}
	if rsvpResp.Msg.Rsvp.Status != pb.RsvpStatus_RSVP_STATUS_GOING {
		t.Errorf("expected GOING status, got %v", rsvpResp.Msg.Rsvp.Status)
	}
	if rsvpResp.Msg.MagicLink == "" {
		t.Fatal("expected non-empty magic_link")
	}

	// 2. List my RSVPs (using magic link token)
	// The magic link is a URL like https://app.federatedmeetup.com/rsvp?token=xxx
	// Extract the token from the URL
	token := rsvpResp.Msg.MagicLink
	if idx := strings.LastIndex(token, "token="); idx >= 0 {
		token = token[idx+6:]
	}
	myRsvpsResp, err := client.ListMyRsvps(ctx, connect.NewRequest(&pb.ListMyRsvpsRequest{
		Email: "dave@example.com",
		Token: token,
	}))
	if err != nil {
		t.Fatalf("ListMyRsvps: %v", err)
	}
	if len(myRsvpsResp.Msg.Rsvps) != 1 {
		t.Fatalf("expected 1 rsvp, got %d", len(myRsvpsResp.Msg.Rsvps))
	}

	// 3. List attendees (should include Dave)
	attendeesResp, err := client.ListAttendees(ctx, connect.NewRequest(&pb.ListAttendeesRequest{
		EventId:        "evt-flow-1",
		OrganizerToken: "test-organizer-token",
	}))
	if err != nil {
		t.Fatalf("ListAttendees: %v", err)
	}
	if len(attendeesResp.Msg.Attendees) != 1 {
		t.Fatalf("expected 1 attendee, got %d", len(attendeesResp.Msg.Attendees))
	}

	// 4. Check in attendee
	checkInResp, err := client.CheckInAttendee(ctx, connect.NewRequest(&pb.CheckInAttendeeRequest{
		EventId:        "evt-flow-1",
		AttendeeEmail:  "dave@example.com",
		OrganizerToken: "test-organizer-token",
	}))
	if err != nil {
		t.Fatalf("CheckInAttendee: %v", err)
	}
	if !checkInResp.Msg.CheckedIn {
		t.Error("expected checked_in=true")
	}

	// 5. Cancel RSVP
	cancelResp, err := client.CancelRsvp(ctx, connect.NewRequest(&pb.CancelRsvpRequest{
		EventId: "evt-flow-1",
		Email:   "dave@example.com",
		Token:   token,
	}))
	if err != nil {
		t.Fatalf("CancelRsvp: %v", err)
	}
	if !cancelResp.Msg.Cancelled {
		t.Error("expected cancelled=true")
	}
}

func TestIntegration_OrganizerDashboard(t *testing.T) {
	ctx := context.Background()
	client, store := setupTestServer(t)

	// Add a completed order for revenue
	store.PutOrder(&pb.Order{
		OrderId:        "order-dash-1",
		TicketId:       "tkt-regular",
		AttendeeEmail:  "revenue@example.com",
		Status:         pb.OrderStatus_ORDER_STATUS_COMPLETED,
		StripeSessionId: "sess-1",
		AmountPaid:     &pb.Money{Amount: 5000, Currency: "USD"},
		CreatedAt:      timestamppb.Now(),
	})

	store.PutOrder(&pb.Order{
		OrderId:        "order-dash-2",
		TicketId:       "tkt-regular",
		AttendeeEmail:  "revenue2@example.com",
		Status:         pb.OrderStatus_ORDER_STATUS_COMPLETED,
		StripeSessionId: "sess-2",
		AmountPaid:     &pb.Money{Amount: 5000, Currency: "USD"},
		CreatedAt:      timestamppb.Now(),
	})

	// Add a pending order
	store.PutOrder(&pb.Order{
		OrderId:        "order-dash-3",
		TicketId:       "tkt-regular",
		AttendeeEmail:  "pending@example.com",
		Status:         pb.OrderStatus_ORDER_STATUS_PENDING,
		StripeSessionId: "sess-3",
		AmountPaid:     &pb.Money{Amount: 5000, Currency: "USD"},
		CreatedAt:      timestamppb.Now(),
	})

	dashResp, err := client.GetOrganizerDashboard(ctx, connect.NewRequest(&pb.GetOrganizerDashboardRequest{
		GroupId:         "grp-yoga",
		OrganizerToken:  "test-organizer-token",
	}))
	if err != nil {
		t.Fatalf("GetOrganizerDashboard: %v", err)
	}
	if len(dashResp.Msg.UpcomingEvents) < 1 {
		t.Errorf("expected >=1 upcoming event, got %d", len(dashResp.Msg.UpcomingEvents))
	}
	if dashResp.Msg.TotalRevenue == nil || dashResp.Msg.TotalRevenue.Amount != 10000 {
		t.Errorf("expected revenue 10000, got %v", dashResp.Msg.TotalRevenue)
	}
}