// SPDX-License-Identifier: AGPL-3.0
//
// Product daemon — mounts the ProductService on a standalone HTTP server.
// If STRIPE_SECRET_KEY is set, uses real Stripe Connect for checkout/refund.
// Otherwise, uses the mock payment provider (for local dev and tests).

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/sscoble/federated-meetup/internal/payment"
	"github.com/sscoble/federated-meetup/internal/product"
	"github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1/federatedmeetupproductv1connect"

	"connectrpc.com/connect"
)

func main() {
	store := product.NewStore()

	// Select payment provider: real Stripe if key is set, mock otherwise.
	var pay payment.Provider
	if payment.HasStripeKey() {
		successURL := getenv("CHECKOUT_SUCCESS_URL", "https://app.federatedmeetup.com/checkout/success")
		cancelURL := getenv("CHECKOUT_CANCEL_URL", "https://app.federatedmeetup.com/checkout/cancel")
		pay = payment.NewStripeConnectProvider(successURL, cancelURL)
		log.Printf("Payment provider: Stripe Connect (key set)")
	} else {
		pay = payment.NewMockProvider()
		log.Printf("Payment provider: mock (set STRIPE_SECRET_KEY for real Stripe)")
	}

	svc := product.NewService(store, pay)

	mux := http.NewServeMux()
	path, handler := federatedmeetupproductv1connect.NewProductServiceHandler(
		svc,
		connect.WithCompressMinBytes(0),
	)
	mux.Handle(path, handler)

	// Mount Stripe webhook handler if configured.
	if payment.IsWebhookConfigured() {
		wh := store.WebhookHandler("")
		mux.Handle("/stripe/webhook", wh)
		log.Printf("Stripe webhook: /stripe/webhook (signature verification enabled)")
	} else {
		log.Printf("Stripe webhook: disabled (set STRIPE_WEBHOOK_SECRET to enable)")
	}

	addr := getenv("PRODUCT_LISTEN_ADDR", ":18081")
	fmt.Printf("ProductService daemon listening on %s\n", addr)
	fmt.Printf("Endpoint: http://127.0.0.1%s%s\n", addr, path)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}