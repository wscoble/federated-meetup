// SPDX-License-Identifier: AGPL-3.0

package payment

import (
	"context"
	"fmt"
	"os"

	stripe "github.com/stripe/stripe-go/v76"
	stripecsession "github.com/stripe/stripe-go/v76/checkout/session"
	striperefund "github.com/stripe/stripe-go/v76/refund"
)

// StripeConnectProvider implements Provider using the Stripe Go SDK.
// It requires STRIPE_SECRET_KEY in the environment.
type StripeConnectProvider struct {
	// successURL and cancelURL are the redirect URLs for checkout.
	successURL string
	cancelURL  string
}

// NewStripeConnectProvider creates a StripeConnectProvider.
// It reads STRIPE_SECRET_KEY from the environment. If not set, it panics
// (the caller should check HasStripeKey() first).
func NewStripeConnectProvider(successURL, cancelURL string) *StripeConnectProvider {
	key := os.Getenv("STRIPE_SECRET_KEY")
	if key == "" {
		panic("STRIPE_SECRET_KEY not set")
	}
	stripe.Key = key
	return &StripeConnectProvider{
		successURL: successURL,
		cancelURL:  cancelURL,
	}
}

// HasStripeKey returns true if STRIPE_SECRET_KEY is set in the environment.
func HasStripeKey() bool {
	return os.Getenv("STRIPE_SECRET_KEY") != ""
}

func (p *StripeConnectProvider) CreateCheckoutSession(ctx context.Context, params CheckoutParams) (string, string, error) {
	params.SuccessURL = p.successURL
	params.CancelURL = p.cancelURL

	currency := params.Currency
	if currency == "" {
		currency = "usd"
	}

	stripeParams := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency:    stripe.String(currency),
					UnitAmount:  stripe.Int64(int64(params.AmountCents)),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String(params.TicketName),
					},
				},
				Quantity: stripe.Int64(int64(params.Quantity)),
			},
		},
		SuccessURL:         stripe.String(params.SuccessURL),
		CancelURL:          stripe.String(params.CancelURL),
		CustomerEmail:      stripe.String(params.AttendeeEmail),
		ClientReferenceID:  stripe.String(params.OrderID),
	}

	// Metadata for reconciliation.
	stripeParams.AddMetadata("ticket_id", params.TicketID)
	stripeParams.AddMetadata("order_id", params.OrderID)
	stripeParams.AddMetadata("attendee_email", params.AttendeeEmail)

	sess, err := stripecsession.New(stripeParams)
	if err != nil {
		return "", "", fmt.Errorf("stripe checkout session: %w", err)
	}

	return sess.ID, sess.URL, nil
}

func (p *StripeConnectProvider) RefundPayment(ctx context.Context, params RefundParams) error {
	stripeParams := &stripe.RefundParams{
		PaymentIntent: stripe.String(params.StripeSessionID),
	}
	if params.AmountCents > 0 {
		stripeParams.Amount = stripe.Int64(int64(params.AmountCents))
	}
	if params.Reason != "" {
		stripeParams.Reason = stripe.String(params.Reason)
	}

	_, err := striperefund.New(stripeParams)
	if err != nil {
		return fmt.Errorf("stripe refund: %w", err)
	}
	return nil
}