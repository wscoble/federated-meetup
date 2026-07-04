// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- Test helpers ---

func newTestService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store := NewStore()
	svc := NewService(store, nil)
	return svc, store
}

func seedEvent(store *Store, eventID, groupID, slug string) *pb.Event {
	e := &pb.Event{
		EventId:    eventID,
		GroupId:    groupID,
		Title:      "Test Event " + eventID,
		Slug:       slug,
		Capacity:   100,
		Visibility: pb.EventVisibility_EVENT_VISIBILITY_PUBLIC,
		StartsAt:   timestamppb.Now(),
	}
	store.PutEvent(e)
	return e
}

func seedTicket(store *Store, eventID, ticketID, name string, capacity uint64, priceAmount uint64) *pb.Ticket {
	t := &pb.Ticket{
		TicketId: ticketID,
		Name:     name,
		Price: &pb.Money{
			Amount:   priceAmount,
			Currency: "USD",
		},
		Capacity: capacity,
		Sold:     0,
	}
	store.PutTicket(eventID, t)
	return t
}

// testOrganizerToken is the standard organizer token used in tests.
const testOrganizerToken = "test-organizer-token"

// seedOrganizerToken registers the test organizer token for the given group.
func seedOrganizerToken(store *Store, groupID string) {
	store.PutOrganizerToken(testOrganizerToken, groupID)
}

// connectReq wraps a proto message pointer in a connect.Request.
func connectReq[T any](msg *T) *connect.Request[T] {
	return connect.NewRequest[T](msg)
}

// --- Ticket lifecycle tests ---

func TestTicketLifecycle_CreateListPurchaseRefund(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt1", "grp1", "test-event")
	seedOrganizerToken(store, "grp1")

	// 1. Create a ticket.
	createResp, err := svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
		EventId:        "evt1",
		OrganizerToken: testOrganizerToken,
		Ticket: &pb.Ticket{
			Name: "Early Bird",
			Price: &pb.Money{
				Amount:   5000,
				Currency: "USD",
			},
			Capacity: 10,
		},
	}))
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	ticketID := createResp.Msg.TicketId
	if ticketID == "" {
		t.Fatal("expected non-empty ticket_id")
	}

	// 2. List tickets for the event.
	listResp, err := svc.ListTickets(ctx, connectReq(&pb.ListTicketsRequest{
		EventId: "evt1",
	}))
	if err != nil {
		t.Fatalf("ListTickets failed: %v", err)
	}
	if len(listResp.Msg.Tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(listResp.Msg.Tickets))
	}
	if listResp.Msg.Tickets[0].TicketId != ticketID {
		t.Fatalf("expected ticket_id %s, got %s", ticketID, listResp.Msg.Tickets[0].TicketId)
	}

	// 3. Purchase a ticket — sold should increment.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      ticketID,
		AttendeeEmail: "alice@example.com",
		AttendeeName:  "Alice",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}
	orderID := purchaseResp.Msg.OrderId
	if orderID == "" {
		t.Fatal("expected non-empty order_id")
	}
	if purchaseResp.Msg.StripeCheckoutUrl == "" {
		t.Fatal("expected non-empty stripe_checkout_url")
	}

	// Verify sold incremented.
	ticket, _ := store.GetTicket(ticketID)
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 after purchase, got %d", ticket.Sold)
	}

	// 4. Refund the order — sold should decrement.
	refundResp, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId:        orderID,
		OrganizerToken: testOrganizerToken,
		Amount:         5000,
		Reason:         "customer request",
	}))
	if err != nil {
		t.Fatalf("RefundOrder failed: %v", err)
	}
	if refundResp.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("expected order status REFUNDED, got %s", refundResp.Msg.Order.Status)
	}
	if refundResp.Msg.Order.RefundedAt == nil {
		t.Fatal("expected refunded_at to be set")
	}

	// Verify sold decremented.
	ticket, _ = store.GetTicket(ticketID)
	if ticket.Sold != 0 {
		t.Fatalf("expected sold=0 after refund, got %d", ticket.Sold)
	}
}

// --- RSVP lifecycle tests ---

func TestRsvpLifecycle_SubmitListCancelCheckIn(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt2", "grp1", "rsvp-event")
	seedOrganizerToken(store, "grp1")

	// 1. Submit RSVP.
	submitResp, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt2",
		Email:   "bob@example.com",
		Name:    "Bob",
	}))
	if err != nil {
		t.Fatalf("SubmitRsvp failed: %v", err)
	}
	if submitResp.Msg.Rsvp == nil {
		t.Fatal("expected non-nil rsvp")
	}
	if submitResp.Msg.Rsvp.Status != pb.RsvpStatus_RSVP_STATUS_GOING {
		t.Fatalf("expected GOING status, got %s", submitResp.Msg.Rsvp.Status)
	}
	magicLink := submitResp.Msg.MagicLink
	if magicLink == "" {
		t.Fatal("expected non-empty magic_link")
	}

	// Extract token from magic link.
	// magic link format: https://app.federatedmeetup.com/rsvp?token=<token>
	parts := strings.SplitN(magicLink, "token=", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected magic link format: %s", magicLink)
	}
	token := parts[1]

	// 2. List my RSVPs.
	listResp, err := svc.ListMyRsvps(ctx, connectReq(&pb.ListMyRsvpsRequest{
		Email: "bob@example.com",
		Token: token,
	}))
	if err != nil {
		t.Fatalf("ListMyRsvps failed: %v", err)
	}
	if len(listResp.Msg.Rsvps) != 1 {
		t.Fatalf("expected 1 rsvp, got %d", len(listResp.Msg.Rsvps))
	}

	// 3. Cancel RSVP.
	cancelResp, err := svc.CancelRsvp(ctx, connectReq(&pb.CancelRsvpRequest{
		EventId: "evt2",
		Email:   "bob@example.com",
		Token:   token,
	}))
	if err != nil {
		t.Fatalf("CancelRsvp failed: %v", err)
	}
	if !cancelResp.Msg.Cancelled {
		t.Fatal("expected cancelled=true")
	}

	// Verify status is NOT_GOING.
	rsvp, _ := store.GetRsvp("evt2", "bob@example.com")
	if rsvp.Status != pb.RsvpStatus_RSVP_STATUS_NOT_GOING {
		t.Fatalf("expected NOT_GOING status, got %s", rsvp.Status)
	}

	// 4. Check-in attendee (set attended=true; needs RSVP to exist).
	// Re-submit RSVP to get a going status again for check-in test.
	submitResp2, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt2",
		Email:   "carol@example.com",
		Name:    "Carol",
	}))
	if err != nil {
		t.Fatalf("SubmitRsvp (carol) failed: %v", err)
	}
	_ = submitResp2

	checkInResp, err := svc.CheckInAttendee(ctx, connectReq(&pb.CheckInAttendeeRequest{
		EventId:        "evt2",
		AttendeeEmail:  "carol@example.com",
		OrganizerToken: testOrganizerToken,
	}))
	if err != nil {
		t.Fatalf("CheckInAttendee failed: %v", err)
	}
	if !checkInResp.Msg.CheckedIn {
		t.Fatal("expected checked_in=true")
	}

	// Verify attended=true.
	rsvp2, _ := store.GetRsvp("evt2", "carol@example.com")
	if !rsvp2.Attended {
		t.Fatal("expected attended=true")
	}
}

// --- Event visibility test ---

func TestEventVisibility_PublicEventAccessibleWithoutAuth(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt3", "grp1", "public-event")
	seedTicket(store, "evt3", "tkt3", "General", 50, 1000)

	// GetPublicEvent by slug — no auth token in request.
	resp, err := svc.GetPublicEvent(ctx, connectReq(&pb.GetPublicEventRequest{
		Slug: "public-event",
	}))
	if err != nil {
		t.Fatalf("GetPublicEvent failed: %v", err)
	}
	if resp.Msg.Event == nil {
		t.Fatal("expected non-nil event")
	}
	if resp.Msg.Event.EventId != "evt3" {
		t.Fatalf("expected event_id evt3, got %s", resp.Msg.Event.EventId)
	}
	if resp.Msg.Capacity != 100 {
		t.Fatalf("expected capacity 100, got %d", resp.Msg.Capacity)
	}
	if len(resp.Msg.Tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(resp.Msg.Tickets))
	}
	if resp.Msg.Tickets[0].TicketId != "tkt3" {
		t.Fatalf("expected ticket_id tkt3, got %s", resp.Msg.Tickets[0].TicketId)
	}

	// Also test by event_id.
	resp2, err := svc.GetPublicEvent(ctx, connectReq(&pb.GetPublicEventRequest{
		EventId: "evt3",
	}))
	if err != nil {
		t.Fatalf("GetPublicEvent by event_id failed: %v", err)
	}
	if resp2.Msg.Event.EventId != "evt3" {
		t.Fatalf("expected event_id evt3, got %s", resp2.Msg.Event.EventId)
	}
}

// --- Organizer dashboard test ---

func TestOrganizerDashboard_CorrectAggregation(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt4", "grp1", "dash-event")
	seedEvent(store, "evt5", "grp1", "dash-event2")
	seedOrganizerToken(store, "grp1")

	// Create tickets.
	t1 := seedTicket(store, "evt4", "tkt4a", "Regular", 100, 5000)
	seedTicket(store, "evt4", "tkt4b", "VIP", 10, 10000)

	// Submit RSVPs for evt4.
	for i := 0; i < 3; i++ {
		svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
			EventId: "evt4",
			Email:   "user" + string(rune('a'+i)) + "@example.com",
			Name:    "User",
		}))
	}

	// Submit 1 RSVP for evt5.
	svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt5",
		Email:   "userd@example.com",
		Name:    "UserD",
	}))

	// Purchase tickets to generate orders.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt4a",
		AttendeeEmail: "buyer@example.com",
		Quantity:      2,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}

	// Mark the order as completed for revenue calculation.
	order, _ := store.GetOrder(purchaseResp.Msg.OrderId)
	order.Status = pb.OrderStatus_ORDER_STATUS_COMPLETED
	store.UpdateOrder(order)

	// Create another completed order.
	purchaseResp2, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt4b",
		AttendeeEmail: "buyer2@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket 2 failed: %v", err)
	}
	order2, _ := store.GetOrder(purchaseResp2.Msg.OrderId)
	order2.Status = pb.OrderStatus_ORDER_STATUS_COMPLETED
	store.UpdateOrder(order2)

	// Get dashboard.
	dashResp, err := svc.GetOrganizerDashboard(ctx, connectReq(&pb.GetOrganizerDashboardRequest{
		GroupId:        "grp1",
		OrganizerToken: testOrganizerToken,
	}))
	if err != nil {
		t.Fatalf("GetOrganizerDashboard failed: %v", err)
	}

	// Check upcoming events.
	if len(dashResp.Msg.UpcomingEvents) != 2 {
		t.Fatalf("expected 2 upcoming events, got %d", len(dashResp.Msg.UpcomingEvents))
	}

	// Check total RSVPs (3 for evt4 + 1 for evt5 = 4).
	if dashResp.Msg.TotalRsvps != 4 {
		t.Fatalf("expected total_rsvps=4, got %d", dashResp.Msg.TotalRsvps)
	}

	// Check total revenue: 2*5000 + 1*10000 = 20000.
	if dashResp.Msg.TotalRevenue == nil {
		t.Fatal("expected non-nil total_revenue")
	}
	if dashResp.Msg.TotalRevenue.Amount != 20000 {
		t.Fatalf("expected total_revenue=20000, got %d", dashResp.Msg.TotalRevenue.Amount)
	}
	if dashResp.Msg.TotalRevenue.Currency != "USD" {
		t.Fatalf("expected currency USD, got %s", dashResp.Msg.TotalRevenue.Currency)
	}

	// Check pending actions: t1 has 2 sold, and the two completed orders have no pending.
	// But there are no pending orders in this test, so pending_actions should be empty.
	// Actually we didn't leave any pending orders. Let's verify.
	// The two orders we created were both set to COMPLETED, so no pending actions.
	if len(dashResp.Msg.PendingActions) != 0 {
		t.Fatalf("expected 0 pending actions, got %d", len(dashResp.Msg.PendingActions))
	}

	_ = t1 // keep reference
}

func TestOrganizerDashboard_PendingActions(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt6", "grp1", "pending-event")
	seedOrganizerToken(store, "grp1")
	seedTicket(store, "evt6", "tkt6", "Regular", 100, 5000)

	// Purchase a ticket (order will be pending by default).
	_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt6",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}

	dashResp, err := svc.GetOrganizerDashboard(ctx, connectReq(&pb.GetOrganizerDashboardRequest{
		GroupId:        "grp1",
		OrganizerToken: testOrganizerToken,
	}))
	if err != nil {
		t.Fatalf("GetOrganizerDashboard failed: %v", err)
	}

	if len(dashResp.Msg.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got %d", len(dashResp.Msg.PendingActions))
	}
}

// --- Edge case tests ---

func TestEdgeCases_MissingEventReturnsNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetPublicEvent(ctx, connectReq(&pb.GetPublicEventRequest{
		EventId: "nonexistent",
	}))
	if err == nil {
		t.Fatal("expected error for missing event")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

func TestEdgeCases_SoldOutTicketReturnsError(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt7", "grp1", "soldout-event")
	// Ticket with capacity 2, already sold 2.
	ticket := &pb.Ticket{
		TicketId: "tkt7",
		Name:     "Limited",
		Price: &pb.Money{
			Amount:   1000,
			Currency: "USD",
		},
		Capacity: 2,
		Sold:     2,
	}
	store.PutTicket("evt7", ticket)

	_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "tkt7",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err == nil {
		t.Fatal("expected error for sold-out ticket")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %s", ce.Code())
	}
}

func TestEdgeCases_CancelRsvpWithWrongTokenReturnsError(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt8", "grp1", "cancel-event")

	// Submit RSVP.
	submitResp, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt8",
		Email:   "dave@example.com",
		Name:    "Dave",
	}))
	if err != nil {
		t.Fatalf("SubmitRsvp failed: %v", err)
	}

	// Extract correct token.
	parts := strings.SplitN(submitResp.Msg.MagicLink, "token=", 2)
	correctToken := parts[1]

	// Cancel with wrong token.
	_, err = svc.CancelRsvp(ctx, connectReq(&pb.CancelRsvpRequest{
		EventId: "evt8",
		Email:   "dave@example.com",
		Token:   "wrong-token",
	}))
	if err == nil {
		t.Fatal("expected error for wrong token")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %s", ce.Code())
	}

	// Cancel with correct token should succeed.
	_, err = svc.CancelRsvp(ctx, connectReq(&pb.CancelRsvpRequest{
		EventId: "evt8",
		Email:   "dave@example.com",
		Token:   correctToken,
	}))
	if err != nil {
		t.Fatalf("CancelRsvp with correct token failed: %v", err)
	}
}

func TestEdgeCases_ListMyRsvpsWithWrongTokenReturnsError(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt9", "grp1", "list-rsvp-event")

	svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt9",
		Email:   "eve@example.com",
		Name:    "Eve",
	}))

	_, err := svc.ListMyRsvps(ctx, connectReq(&pb.ListMyRsvpsRequest{
		Email: "eve@example.com",
		Token: "wrong-token",
	}))
	if err == nil {
		t.Fatal("expected error for wrong token")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %s", ce.Code())
	}
}

func TestEdgeCases_GetPublicEventByNonExistentSlug(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetPublicEvent(ctx, connectReq(&pb.GetPublicEventRequest{
		Slug: "nonexistent-slug",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent slug")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// Test ListUpcomingEvents pagination.
func TestListUpcomingEvents_Pagination(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Create 5 events.
	for i := 0; i < 5; i++ {
		seedEvent(store, "page-evt-"+string(rune('a'+i)), "grp1", "page-event-"+string(rune('a'+i)))
	}

	// Page size 2.
	resp, err := svc.ListUpcomingEvents(ctx, connectReq(&pb.ListUpcomingEventsRequest{
		GroupId:  "grp1",
		PageSize: 2,
	}))
	if err != nil {
		t.Fatalf("ListUpcomingEvents failed: %v", err)
	}
	if len(resp.Msg.Events) != 2 {
		t.Fatalf("expected 2 events in first page, got %d", len(resp.Msg.Events))
	}
	if resp.Msg.NextCursor == "" {
		t.Fatal("expected non-empty next_cursor")
	}

	// Second page.
	resp2, err := svc.ListUpcomingEvents(ctx, connectReq(&pb.ListUpcomingEventsRequest{
		GroupId:  "grp1",
		PageSize: 2,
		Cursor:   resp.Msg.NextCursor,
	}))
	if err != nil {
		t.Fatalf("ListUpcomingEvents page 2 failed: %v", err)
	}
	if len(resp2.Msg.Events) != 2 {
		t.Fatalf("expected 2 events in second page, got %d", len(resp2.Msg.Events))
	}

	// Third page should have 1 event.
	resp3, err := svc.ListUpcomingEvents(ctx, connectReq(&pb.ListUpcomingEventsRequest{
		GroupId:  "grp1",
		PageSize: 2,
		Cursor:   resp2.Msg.NextCursor,
	}))
	if err != nil {
		t.Fatalf("ListUpcomingEvents page 3 failed: %v", err)
	}
	if len(resp3.Msg.Events) != 1 {
		t.Fatalf("expected 1 event in third page, got %d", len(resp3.Msg.Events))
	}
	if resp3.Msg.NextCursor != "" {
		t.Fatalf("expected empty next_cursor on last page, got %s", resp3.Msg.NextCursor)
	}
}

// Test ListOrders pagination.
func TestListOrders_Pagination(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt-ord", "grp1", "orders-event")
	seedOrganizerToken(store, "grp1")
	seedTicket(store, "evt-ord", "tkt-ord", "Regular", 100, 1000)

	// Create 3 orders.
	for i := 0; i < 3; i++ {
		svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
			TicketId:      "tkt-ord",
			AttendeeEmail: "buyer" + string(rune('a'+i)) + "@example.com",
			Quantity:      1,
		}))
	}

	// Page size 2.
	resp, err := svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
		EventId:        "evt-ord",
		OrganizerToken: testOrganizerToken,
		PageSize:       2,
	}))
	if err != nil {
		t.Fatalf("ListOrders failed: %v", err)
	}
	if len(resp.Msg.Orders) != 2 {
		t.Fatalf("expected 2 orders in first page, got %d", len(resp.Msg.Orders))
	}
	if resp.Msg.NextCursor == "" {
		t.Fatal("expected non-empty next_cursor")
	}

	// Second page.
	resp2, err := svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
		EventId:        "evt-ord",
		OrganizerToken: testOrganizerToken,
		PageSize:       2,
		Cursor:         resp.Msg.NextCursor,
	}))
	if err != nil {
		t.Fatalf("ListOrders page 2 failed: %v", err)
	}
	if len(resp2.Msg.Orders) != 1 {
		t.Fatalf("expected 1 order in second page, got %d", len(resp2.Msg.Orders))
	}
	if resp2.Msg.NextCursor != "" {
		t.Fatalf("expected empty next_cursor on last page, got %s", resp2.Msg.NextCursor)
	}
}

// Test CreateTicket for non-existent event returns NotFound.
func TestCreateTicket_EventNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
		EventId:        "nonexistent",
		OrganizerToken: testOrganizerToken,
		Ticket: &pb.Ticket{
			Name: "Test",
		},
	}))
	if err == nil {
		t.Fatal("expected error for non-existent event")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// Test SubmitRsvp for non-existent event returns NotFound.
func TestSubmitRsvp_EventNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "nonexistent",
		Email:   "test@example.com",
		Name:    "Test",
	}))
	if err == nil {
		t.Fatal("expected error for non-existent event")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// Test CheckInAttendee for non-existent RSVP returns NotFound.
func TestCheckInAttendee_RsvpNotFound(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt-chk", "grp1", "checkin-event")
	seedOrganizerToken(store, "grp1")

	_, err := svc.CheckInAttendee(ctx, connectReq(&pb.CheckInAttendeeRequest{
		EventId:        "evt-chk",
		AttendeeEmail:  "nonexistent@example.com",
		OrganizerToken: testOrganizerToken,
	}))
	if err == nil {
		t.Fatal("expected error for non-existent RSVP")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// Test RefundOrder for non-existent order returns NotFound.
func TestRefundOrder_OrderNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId:        "nonexistent",
		OrganizerToken: testOrganizerToken,
		Amount:         1000,
	}))
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// Test ListAttendees returns only Going RSVPs.
func TestListAttendees_OnlyGoing(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "evt-att", "grp1", "attendees-event")
	seedOrganizerToken(store, "grp1")

	// Submit 2 RSVPs (going).
	svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt-att",
		Email:   "going1@example.com",
		Name:    "Going1",
	}))
	submitResp2, _ := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
		EventId: "evt-att",
		Email:   "going2@example.com",
		Name:    "Going2",
	}))

	// Cancel one RSVP (set to NOT_GOING).
	parts := strings.SplitN(submitResp2.Msg.MagicLink, "token=", 2)
	token := parts[1]
	svc.CancelRsvp(ctx, connectReq(&pb.CancelRsvpRequest{
		EventId: "evt-att",
		Email:   "going2@example.com",
		Token:   token,
	}))

	resp, err := svc.ListAttendees(ctx, connectReq(&pb.ListAttendeesRequest{
		EventId:        "evt-att",
		OrganizerToken: testOrganizerToken,
	}))
	if err != nil {
		t.Fatalf("ListAttendees failed: %v", err)
	}
	// Should be 1 (going1 is going, going2 cancelled).
	if len(resp.Msg.Attendees) != 1 {
		t.Fatalf("expected 1 attendee, got %d", len(resp.Msg.Attendees))
	}
	if resp.Msg.Attendees[0].UserEmail != "going1@example.com" {
		t.Fatalf("expected going1@example.com, got %s", resp.Msg.Attendees[0].UserEmail)
	}
}