// SPDX-License-Identifier: AGPL-3.0

// Package payment abstracts the payment provider (Stripe Connect) behind an
// interface so the product service can be tested with a mock and deployed with
// real Stripe without changing call sites.
//
// The interface covers the two operations the product service needs:
//   - CreateCheckoutSession: creates a Stripe Checkout Session for a ticket purchase
//   - RefundPayment: issues a full or partial refund on a completed charge
//
// The StripeConnect provider implements this with real Stripe API calls.
// The Mock provider returns deterministic fake URLs (for tests and local dev).
package payment

import (
	"context"
	"fmt"
)

// Provider is the payment abstraction used by the product service.
type Provider interface {
	// CreateCheckoutSession creates a checkout session for a ticket purchase.
	// Returns the session ID and the URL the attendee should be redirected to.
	CreateCheckoutSession(ctx context.Context, params CheckoutParams) (sessionID string, checkoutURL string, err error)

	// RefundPayment issues a refund for a previously captured charge.
	// amount=0 means full refund. A non-zero amount is a partial refund in cents.
	RefundPayment(ctx context.Context, params RefundParams) error
}

// CheckoutParams holds the data needed to create a Stripe Checkout Session.
type CheckoutParams struct {
	TicketID      string
	TicketName    string
	AmountCents   uint64 // total amount in cents (price * quantity)
	Currency      string // ISO 4217 lowercase, e.g. "usd"
	Quantity      uint64
	AttendeeEmail string
	OrderID       string // used in metadata for reconciliation
	SuccessURL    string // redirect URL after successful payment
	CancelURL     string // redirect URL if the user cancels
}

// RefundParams holds the data needed to issue a refund.
type RefundParams struct {
	StripeSessionID string // the Stripe session/charge ID to refund
	AmountCents     uint64 // 0 = full refund; >0 = partial refund
	Reason          string // "requested_by_customer", "duplicate", "fraudulent"
}

// MockProvider is a no-op provider that returns deterministic fake URLs.
// Used in tests and local development. It never calls Stripe.
type MockProvider struct{}

func (m *MockProvider) CreateCheckoutSession(ctx context.Context, params CheckoutParams) (string, string, error) {
	sessionID := fmt.Sprintf("mock_sess_%s", params.OrderID)
	checkoutURL := fmt.Sprintf("https://checkout.stripe.com/c/pay/mock_%s", params.OrderID)
	return sessionID, checkoutURL, nil
}

func (m *MockProvider) RefundPayment(ctx context.Context, params RefundParams) error {
	// Mock refund always succeeds.
	return nil
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider() *MockProvider {
	return &MockProvider{}
}