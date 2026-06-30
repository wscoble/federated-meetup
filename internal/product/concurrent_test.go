// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ---------------------------------------------------------------------------
// Concurrent purchase tests
// ---------------------------------------------------------------------------

// TestConcurrentPurchase_LimitedCapacity verifies that when N goroutines all
// try to purchase the same ticket with capacity=1, exactly one succeeds and the
// rest get a sold-out error. This would fail with a data race or
// over-selling if PurchaseTicket had a TOCTOU bug.
func TestConcurrentPurchase_LimitedCapacity(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "concurrent-evt-1", "grp1", "concurrent-event-1")
	seedTicket(store, "concurrent-evt-1", "concurrent-tkt-1", "Limited", 1, 5000)

	const N = 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
				TicketId:      "concurrent-tkt-1",
				AttendeeEmail: fmt.Sprintf("buyer%d@example.com", idx),
				Quantity:      1,
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful purchase, got %d", successCount)
	}
	if errorCount != N-1 {
		t.Fatalf("expected %d errors, got %d", N-1, errorCount)
	}

	// Verify sold is exactly 1.
	ticket, _ := store.GetTicket("concurrent-tkt-1")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1, got %d", ticket.Sold)
	}
}

// TestConcurrentPurchase_SmallCapacity verifies that when N goroutines try to
// purchase a ticket with capacity=5, exactly 5 succeed and the rest fail.
func TestConcurrentPurchase_SmallCapacity(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	const capacity = uint64(5)
	seedEvent(store, "concurrent-evt-2", "grp1", "concurrent-event-2")
	seedTicket(store, "concurrent-evt-2", "concurrent-tkt-2", "Limited5", capacity, 5000)

	const N = 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
				TicketId:      "concurrent-tkt-2",
				AttendeeEmail: fmt.Sprintf("buyer%d@example.com", idx),
				Quantity:      1,
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if successCount != int64(capacity) {
		t.Fatalf("expected %d successful purchases, got %d", capacity, successCount)
	}
	if errorCount != N-int64(capacity) {
		t.Fatalf("expected %d errors, got %d", N-int64(capacity), errorCount)
	}

	// Verify sold is exactly capacity.
	ticket, _ := store.GetTicket("concurrent-tkt-2")
	if ticket.Sold != capacity {
		t.Fatalf("expected sold=%d, got %d", capacity, ticket.Sold)
	}
}

// TestConcurrentPurchase_LargerQuantity verifies that purchasing multiple
// tickets per request is also race-free.
func TestConcurrentPurchase_LargerQuantity(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	const capacity = uint64(10)
	seedEvent(store, "concurrent-evt-3", "grp1", "concurrent-event-3")
	seedTicket(store, "concurrent-evt-3", "concurrent-tkt-3", "BatchCap", capacity, 5000)

	const N = 20
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
				TicketId:      "concurrent-tkt-3",
				AttendeeEmail: fmt.Sprintf("buyer%d@example.com", idx),
				Quantity:      2,
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}
	wg.Wait()

	// With capacity=10 and quantity=2, at most 5 purchases succeed.
	maxSuccess := capacity / 2
	if successCount > int64(maxSuccess) {
		t.Fatalf("expected at most %d successful purchases, got %d", maxSuccess, successCount)
	}
	if successCount+errorCount != N {
		t.Fatalf("expected %d total, got success=%d + error=%d", N, successCount, errorCount)
	}

	ticket, _ := store.GetTicket("concurrent-tkt-3")
	if ticket.Sold > capacity {
		t.Fatalf("expected sold <= %d, got %d", capacity, ticket.Sold)
	}
	if ticket.Sold != uint64(successCount*2) {
		t.Fatalf("expected sold=%d (successCount*2), got %d", successCount*2, ticket.Sold)
	}
}

// ---------------------------------------------------------------------------
// Concurrent RSVP tests
// ---------------------------------------------------------------------------

// TestConcurrentRsvp_SameEvent spawns N goroutines all submitting RSVPs to the
// same event with different emails. All should succeed and the store should
// have N RSVPs at the end. Run with -race to detect data races.
func TestConcurrentRsvp_SameEvent(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "concurrent-rsvp-evt", "grp1", "concurrent-rsvp-event")

	const N = 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
				EventId: "concurrent-rsvp-evt",
				Email:   fmt.Sprintf("rsvp%d@example.com", idx),
				Name:    fmt.Sprintf("Rsvp%d", idx),
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if successCount != N {
		t.Fatalf("expected %d successful RSVPs, got %d (errors=%d)", N, successCount, errorCount)
	}

	rsvps := store.RsvpsForEvent("concurrent-rsvp-evt")
	if len(rsvps) != N {
		t.Fatalf("expected %d RSVPs in store, got %d", N, len(rsvps))
	}
}

// TestConcurrentRsvp_SameEmail verifies that concurrent RSVPs with the same
// email to the same event are deduplicated properly.
func TestConcurrentRsvp_SameEmail(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "concurrent-rsvp-dedup-evt", "grp1", "concurrent-rsvp-dedup-event")

	const N = 20
	var wg sync.WaitGroup
	var successCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.SubmitRsvp(ctx, connectReq(&pb.SubmitRsvpRequest{
				EventId: "concurrent-rsvp-dedup-evt",
				Email:   "same@example.com",
				Name:    "Same",
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	// All should succeed (existing RSVP returns the existing one with a new token).
	if successCount != N {
		t.Fatalf("expected %d successful responses, got %d", N, successCount)
	}

	// But only 1 RSVP should exist in the store.
	rsvps := store.RsvpsForEvent("concurrent-rsvp-dedup-evt")
	if len(rsvps) != 1 {
		t.Fatalf("expected 1 RSVP in store, got %d", len(rsvps))
	}
}

// ---------------------------------------------------------------------------
// Concurrent reader/writer tests
// ---------------------------------------------------------------------------

// TestConcurrent_ListOrdersWhilePurchasing runs ListOrders (reader) and
// PurchaseTicket (writer) concurrently. The race detector should catch any
// data races in the store's read/write patterns.
func TestConcurrent_ListOrdersWhilePurchasing(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "rw-evt", "grp1", "rw-event")
	seedOrganizerToken(store, "grp1")
	seedTicket(store, "rw-evt", "rw-tkt", "Regular", 1000, 5000)

	// Pre-seed some orders so ListOrders has data.
	for i := 0; i < 10; i++ {
		svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
			TicketId:      "rw-tkt",
			AttendeeEmail: fmt.Sprintf("seed%d@example.com", i),
			Quantity:      1,
		}))
	}

	const writers = 20
	const readers = 20
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers: keep purchasing tickets.
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
				TicketId:      "rw-tkt",
				AttendeeEmail: fmt.Sprintf("writer%d@example.com", idx),
				Quantity:      1,
			}))
		}(i)
	}

	// Readers: keep listing orders.
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.ListOrders(ctx, connectReq(&pb.ListOrdersRequest{
				EventId:        "rw-evt",
				OrganizerToken: testOrganizerToken,
			}))
		}()
	}

	wg.Wait()

	// Verify the final state is consistent: sold should equal total orders.
	ticket, _ := store.GetTicket("rw-tkt")
	orders := store.OrdersForEvent("rw-evt")
	if ticket.Sold != uint64(len(orders)) {
		t.Fatalf("sold=%d but orders count=%d", ticket.Sold, len(orders))
	}
}

// TestConcurrent_CreateTicketAndListTickets runs CreateTicket (writer) and
// ListTickets (reader) concurrently for the same event.
func TestConcurrent_CreateTicketAndListTickets(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "create-list-evt", "grp1", "create-list-event")
	seedOrganizerToken(store, "grp1")

	const writers = 20
	const readers = 20
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers: create tickets.
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			svc.CreateTicket(ctx, connectReq(&pb.CreateTicketRequest{
				EventId:        "create-list-evt",
				OrganizerToken: testOrganizerToken,
				Ticket: &pb.Ticket{
					Name: fmt.Sprintf("Ticket-%d", idx),
					Price: &pb.Money{
						Amount:   1000,
						Currency: "USD",
					},
					Capacity: 10,
				},
			}))
		}(i)
	}

	// Readers: list tickets.
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.ListTickets(ctx, connectReq(&pb.ListTicketsRequest{
				EventId: "create-list-evt",
			}))
		}()
	}

	wg.Wait()

	// Verify all tickets were created.
	tickets := store.TicketsForEvent("create-list-evt")
	if len(tickets) != writers {
		t.Fatalf("expected %d tickets, got %d", writers, len(tickets))
	}
}

// ---------------------------------------------------------------------------
// Concurrent refund tests
// ---------------------------------------------------------------------------

// TestConcurrentRefund_SameOrder verifies that concurrent refunds on the same
// order are handled correctly — only one should succeed, the sold count should
// only be decremented once.
func TestConcurrentRefund_SameOrder(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	seedEvent(store, "concurrent-refund-evt", "grp1", "concurrent-refund-event")
	seedOrganizerToken(store, "grp1")
	seedTicket(store, "concurrent-refund-evt", "concurrent-refund-tkt", "Refundable", 10, 5000)

	// Purchase one ticket.
	purchaseResp, err := svc.PurchaseTicket(ctx, connectReq(&pb.PurchaseTicketRequest{
		TicketId:      "concurrent-refund-tkt",
		AttendeeEmail: "buyer@example.com",
		Quantity:      1,
	}))
	if err != nil {
		t.Fatalf("PurchaseTicket failed: %v", err)
	}
	orderID := purchaseResp.Msg.OrderId

	// Verify sold=1.
	ticket, _ := store.GetTicket("concurrent-refund-tkt")
	if ticket.Sold != 1 {
		t.Fatalf("expected sold=1 before refunds, got %d", ticket.Sold)
	}

	const N = 20
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.RefundOrder(ctx, connectReq(&pb.RefundOrderRequest{
				OrderId:        orderID,
				OrganizerToken: testOrganizerToken,
				Amount:         5000,
				Reason:         "concurrent refund test",
			}))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}()
	}
	wg.Wait()

	// Exactly one refund should succeed.
	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful refund, got %d", successCount)
	}
	if errorCount != N-1 {
		t.Fatalf("expected %d refund errors, got %d", N-1, errorCount)
	}

	// Sold should be 0 (decremented exactly once).
	ticket, _ = store.GetTicket("concurrent-refund-tkt")
	if ticket.Sold != 0 {
		t.Fatalf("expected sold=0 after refund, got %d", ticket.Sold)
	}
}