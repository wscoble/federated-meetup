// SPDX-License-Identifier: AGPL-3.0

package payment

import (
	"context"
	"testing"
)

func TestMockProvider_CreateCheckoutSession(t *testing.T) {
	p := NewMockProvider()
	sessionID, checkoutURL, err := p.CreateCheckoutSession(context.Background(), CheckoutParams{
		TicketID:      "tkt-123",
		TicketName:    "Early Bird",
		AmountCents:   5000,
		Currency:      "usd",
		Quantity:      1,
		AttendeeEmail: "alice@example.com",
		OrderID:       "ord-456",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sessionID == "" {
		t.Error("sessionID should not be empty")
	}
	if checkoutURL == "" {
		t.Error("checkoutURL should not be empty")
	}
	if sessionID != "mock_sess_ord-456" {
		t.Errorf("sessionID = %q, want mock_sess_ord-456", sessionID)
	}
}

func TestMockProvider_RefundPayment(t *testing.T) {
	p := NewMockProvider()
	err := p.RefundPayment(context.Background(), RefundParams{
		StripeSessionID: "sess-123",
		AmountCents:     0, // full refund
		Reason:          "requested_by_customer",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMockProvider_RefundPayment_Partial(t *testing.T) {
	p := NewMockProvider()
	err := p.RefundPayment(context.Background(), RefundParams{
		StripeSessionID: "sess-123",
		AmountCents:     2500, // partial refund
		Reason:          "duplicate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHasStripeKey_NotSet(t *testing.T) {
	// STRIPE_SECRET_KEY should not be set in test env.
	if HasStripeKey() {
		t.Error("HasStripeKey should be false when STRIPE_SECRET_KEY is not set")
	}
}