// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ---------------------------------------------------------------------------
// Payment edge-case tests
// ---------------------------------------------------------------------------

// TestPaymentEdge_RefundAlreadyRefundedOrder verifies that refunding an order
// that is already refunded returns an error (FailedPrecondition) and does NOT
// decrement the ticket sold count a second time.
func TestPaymentEdge_RefundAlreadyRefundedOrder(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "edge-refund-evt", "grp1", "edge-refund-event")
	seedTicket(store, "edge-refund-evt", "edge-refund-tkt", "Edge", 10, 5000)

	// Purchase one ticket.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "edge-refund-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}
	orderID := purchaseResp.Msg.OrderId

	// Verify sold=1.
	ticket, _ := store.GetTicket("edge-refund-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 after purchase, got %d", ticket.Sold)
	}

	// First refund — should succeed.
	_, err = svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orderID,
		Amount:  5000,
		Reason:  "first refund",
	}))
	if err != nil {
		t.Fatalf("first RefundOrder failed: %v", err)
	}

	// Verify sold=0 after first refund.
	ticket, _ = store.GetTicket("edge-refund-tkt")
	if ticket.Sold != 0 {
		t.Fatalf("expected sold=0 after first refund, got %d", ticket.Sold)
	}

	// Second refund — should return an error (already refunded).
	_, err = svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orderID,
		Amount:  5000,
		Reason:  "second refund",
	}))
	if err == nil {
		t.Fatal("expected error for double refund, got nil")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition for double refund, got %s", ce.Code())
	}

	// Verify sold is still 0 (not decremented again).
	ticket, _ = store.GetTicket("edge-refund-tkt")
	if ticket.Sold != 0 {
		t.Fatalf("expected sold=0 after double refund (no second decrement), got %d", ticket.Sold)
	}
}

// TestPaymentEdge_PartialRefund verifies that the refund system handles
// partial refund amounts. Currently RefundOrder fully refunds regardless of
// the amount field. This test documents that behavior: a partial refund amount
// still triggers a full refund (sold decremented by 1, status=REFUNDED). This
// is a known limitation — a proper partial refund would set status to
// PARTIALLY_REFUNDED and only decrement proportionally.
func TestPaymentEdge_PartialRefund(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "partial-refund-evt", "grp1", "partial-refund-event")
	seedTicket(store, "partial-refund-evt", "partial-refund-tkt", "Partial", 10, 10000)

	// Purchase 2 tickets (amount = 2 * 10000 = 20000).
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "partial-refund-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      2,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}
	orderID := purchaseResp.Msg.OrderId

	// Verify order amount.
	order, _ := store.GetOrder(orderID)
	if order.AmountPaid.Amount != 20000 {
		t.Fatalf("expected amount_paid=20000, got %d", order.AmountPaid.Amount)
	}

	// Verify sold=2.
	ticket, _ := store.GetTicket("partial-refund-tkt")
	if ticket.Sold != 2 {
		t.Fatalf("expected sold=2 after purchase, got %d", ticket.Sold)
	}

	// Attempt a partial refund of 5000 (less than the 20000 total).
	// The current implementation ignores the amount field and fully refunds.
	refundResp, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orderID,
		Amount:  5000, // partial — half the total
		Reason:  "partial refund",
	}))
	if err != nil {
		t.Fatalf("RefundOrder failed: %v", err)
	}

	// Current behavior: the order is marked as fully REFUNDED.
	if refundResp.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("expected REFUNDED status (current full-refund behavior), got %s",
			refundResp.Msg.Order.Status)
	}

	// Current behavior: sold is decremented by 1 (not proportionally).
	ticket, _ = store.GetTicket("partial-refund-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 after partial refund (current behavior decrements by 1), got %d",
			ticket.Sold)
	}

	// Document the known limitation: the amount field is ignored.
	// A future implementation should set status=PARTIALLY_REFUNDED and
	// track the remaining refundable amount.
	t.Logf("NOTE: RefundOrder currently ignores the amount field and fully refunds. " +
		"A proper partial refund would set status=PARTIALLY_REFUNDED and only decrement proportionally.")
}

// TestPaymentEdge_PurchaseQuantityExceedsCapacity verifies that purchasing
// with a quantity greater than the remaining capacity returns an error.
func TestPaymentEdge_PurchaseQuantityExceedsCapacity(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "qty-cap-evt", "grp1", "qty-cap-event")
	seedTicket(store, "qty-cap-evt", "qty-cap-tkt", "Small", 5, 1000)

	// Purchase 3 (capacity=5, should succeed).
	_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "qty-cap-tkt",
		AttendeeEmail: "buyer1@example.com",
		Quantity:      3,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket (qty=3) should have succeeded: %v", err)
	}

	// Verify sold=3.
	ticket, _ := store.GetTicket("qty-cap-tkt")
	if ticket.Sold != 3 {
		t.Fatalf("expected sold=3, got %d", ticket.Sold)
	}

	// Try to purchase 3 more (would make sold=6 > capacity=5).
	_, err = svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "qty-cap-tkt",
		AttendeeEmail: "buyer2@example.com",
		Quantity:      3,
	}))
	if err == nil {
		t.Fatal("expected error for quantity exceeding remaining capacity")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %s", ce.Code())
	}

	// Verify sold is still 3.
	ticket, _ = store.GetTicket("qty-cap-tkt")
	if ticket.Sold != 3 {
		t.Fatalf("expected sold=3 (unchanged), got %d", ticket.Sold)
	}

	// Purchase 2 more (sold would become 5 = capacity, should succeed).
	_, err = svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "qty-cap-tkt",
		AttendeeEmail: "buyer3@example.com",
		Quantity:      2,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket (qty=2, fills to capacity) should have succeeded: %v", err)
	}

	// Verify sold=5 (at capacity).
	ticket, _ = store.GetTicket("qty-cap-tkt")
	if ticket.Sold != 5 {
		t.Fatalf("expected sold=5 (at capacity), got %d", ticket.Sold)
	}
}

// TestPaymentEdge_PurchaseSoldOutTicket verifies that purchasing a sold-out
// ticket returns a FailedPrecondition error.
func TestPaymentEdge_PurchaseSoldOutTicket(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "sold-out-evt", "grp1", "sold-out-event")
	seedTicket(store, "sold-out-evt", "sold-out-tkt", "SoldOut", 1, 1000)

	// Purchase the only available ticket.
	_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "sold-out-tkt",
		AttendeeEmail: "buyer1@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("first PurchaseTicket failed: %v", err)
	}

	// Attempt to purchase another — should fail.
	_, err = svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "sold-out-tkt",
		AttendeeEmail: "buyer2@example.com",
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

	// Verify sold is still 1.
	ticket, _ := store.GetTicket("sold-out-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1, got %d", ticket.Sold)
	}
}

// TestPaymentEdge_RefundNonExistentOrder verifies that refunding an order that
// doesn't exist returns a NotFound error.
func TestPaymentEdge_RefundNonExistentOrder(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: "nonexistent-order-id",
		Amount:  1000,
		Reason:  "test",
	}))
	if err == nil {
		t.Fatal("expected error for non-existent order refund")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %s", ce.Code())
	}
}

// TestPaymentEdge_RefundDecrementsSoldCount verifies that after a refund, the
// ticket's sold count is decremented.
func TestPaymentEdge_RefundDecrementsSoldCount(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "refund-decrement-evt", "grp1", "refund-decrement-event")
	seedTicket(store, "refund-decrement-evt", "refund-decrement-tkt", "Decrement", 10, 5000)

	// Purchase 3 tickets.
	for i := 0; i < 3; i++ {
		_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
			TicketId:      "refund-decrement-tkt",
			AttendeeEmail: "buyer" + string(rune('a'+i)) + "@example.com",
			Quantity:      1,
		}))
		if err != nil {
			t.Fatalf("PurchaseTicket %d failed: %v", i, err)
		}
	}

	// Verify sold=3.
	ticket, _ := store.GetTicket("refund-decrement-tkt")
	if ticket.Sold != 3 {
		t.Fatalf("expected sold=3 after purchases, got %d", ticket.Sold)
	}

	// Get one order ID to refund.
	orders := store.OrdersForEvent("refund-decrement-evt")
	if len(orders) != 3 {
		t.Fatalf("expected 3 orders, got %d", len(orders))
	}

	// Refund one order.
	_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orders[0].OrderId,
		Amount:  5000,
		Reason:  "test decrement",
	}))
	if err != nil {
		t.Fatalf("RefundOrder failed: %v", err)
	}

	// Verify sold=2 (decremented by 1).
	ticket, _ = store.GetTicket("refund-decrement-tkt")
	if ticket.Sold != 2 {
		t.Fatalf("expected sold=2 after one refund, got %d", ticket.Sold)
	}

	// Verify the refunded order has REFUNDED status.
	refundedOrder, _ := store.GetOrder(orders[0].OrderId)
	if refundedOrder.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("expected REFUNDED status, got %s", refundedOrder.Status)
	}
	if refundedOrder.RefundedAt == nil {
		t.Fatal("expected refunded_at to be set")
	}
}

// TestPaymentEdge_DoubleRefundDoesNotDecrementTwice verifies that calling
// RefundOrder twice on the same order only decrements the sold count once.
func TestPaymentEdge_DoubleRefundDoesNotDecrementTwice(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "double-refund-evt", "grp1", "double-refund-event")
	seedTicket(store, "double-refund-evt", "double-refund-tkt", "Double", 10, 5000)

	// Purchase 2 tickets.
	for i := 0; i < 2; i++ {
		_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
			TicketId:      "double-refund-tkt",
			AttendeeEmail: "buyer" + string(rune('a'+i)) + "@example.com",
			Quantity:      1,
		}))
		if err != nil {
			t.Fatalf("PurchaseTicket %d failed: %v", i, err)
		}
	}

	// Verify sold=2.
	ticket, _ := store.GetTicket("double-refund-tkt")
	if ticket.Sold != 2 {
		t.Fatalf("expected sold=2 after purchases, got %d", ticket.Sold)
	}

	orders := store.OrdersForEvent("double-refund-evt")
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders, got %d", len(orders))
	}

	// First refund — should succeed and decrement sold to 1.
	_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orders[0].OrderId,
		Amount:  5000,
	}))
	if err != nil {
		t.Fatalf("first RefundOrder failed: %v", err)
	}

	ticket, _ = store.GetTicket("double-refund-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 after first refund, got %d", ticket.Sold)
	}

	// Second refund on same order — should fail and NOT decrement sold again.
	_, err = svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: orders[0].OrderId,
		Amount:  5000,
	}))
	if err == nil {
		t.Fatal("expected error for double refund")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %s", ce.Code())
	}

	// Verify sold is still 1 (not decremented again).
	ticket, _ = store.GetTicket("double-refund-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 (not decremented twice), got %d", ticket.Sold)
	}
}

// TestPaymentEdge_PurchaseWithNilPrice verifies that purchasing a ticket with no
// price set (nil Price) results in an order with amount=0.
func TestPaymentEdge_PurchaseWithNilPrice(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "nil-price-evt", "grp1", "nil-price-event")
	// Create a ticket with nil price (free ticket).
	store.PutTicket("nil-price-evt", &pb.Ticket{
		TicketId: "nil-price-tkt",
		Name:     "Free",
		Price:    nil,
		Capacity: 10,
		Sold:     0,
	})

	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "nil-price-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket with nil price failed: %v", err)
	}

	order, _ := store.GetOrder(purchaseResp.Msg.OrderId)
	if order.AmountPaid == nil {
		t.Fatal("expected non-nil amount_paid")
	}
	if order.AmountPaid.Amount != 0 {
		t.Fatalf("expected amount=0 for nil price ticket, got %d", order.AmountPaid.Amount)
	}
	if order.AmountPaid.Currency != "USD" {
		t.Fatalf("expected currency=USD (default), got %s", order.AmountPaid.Currency)
	}
}

// TestPaymentEdge_PurchaseQuantityZero verifies that purchasing with quantity=0
// defaults to quantity=1 (the existing service behavior).
func TestPaymentEdge_PurchaseQuantityZero(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "qty-zero-evt", "grp1", "qty-zero-event")
	seedTicket(store, "qty-zero-evt", "qty-zero-tkt", "QtyZero", 10, 1000)

	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "qty-zero-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      0, // should default to 1
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket with quantity=0 failed: %v", err)
	}

	// Verify sold=1 (defaulted from 0).
	ticket, _ := store.GetTicket("qty-zero-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 (quantity defaulted to 1), got %d", ticket.Sold)
	}

	// Verify order amount = 1 * 1000 = 1000.
	order, _ := store.GetOrder(purchaseResp.Msg.OrderId)
	if order.AmountPaid.Amount != 1000 {
		t.Fatalf("expected amount=1000 (1 ticket * 1000), got %d", order.AmountPaid.Amount)
	}
}

// TestPaymentEdge_RefundOrderWithEmptyId verifies that refunding with an empty
// order_id returns InvalidArgument.
func TestPaymentEdge_RefundOrderWithEmptyId(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: "",
		Amount:  1000,
	}))
	if err == nil {
		t.Fatal("expected error for empty order_id")
	}
	ce, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %s", ce.Code())
	}
}

// TestPaymentEdge_RefundOrderWithMissingTicket verifies that refunding an order
// whose ticket has been deleted doesn't panic (sold decrement is skipped
// gracefully).
func TestPaymentEdge_RefundOrderWithMissingTicket(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "missing-tkt-evt", "grp1", "missing-tkt-event")
	seedTicket(store, "missing-tkt-evt", "missing-tkt-ref-tkt", "Temp", 10, 5000)

	// Purchase a ticket.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "missing-tkt-ref-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}

	// Delete the ticket from the store (simulating a data loss / cleanup).
	store.DeleteTicket("missing-tkt-ref-tkt")

	// Refund should still succeed (status changes to REFUNDED, but sold
	// decrement is skipped because the ticket is gone).
	_, err = svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
		OrderId: purchaseResp.Msg.OrderId,
		Amount:  5000,
		Reason:  "ticket deleted",
	}))
	if err != nil {
		t.Fatalf("RefundOrder with missing ticket should still succeed: %v", err)
	}

	// Verify the order is refunded.
	order, _ := store.GetOrder(purchaseResp.Msg.OrderId)
	if order.Status != pb.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("expected REFUNDED status, got %s", order.Status)
	}
}