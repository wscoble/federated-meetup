// SPDX-License-Identifier: AGPL-3.0

package payment

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"
)

// WebhookOrderUpdater is the interface the webhook handler calls to update
// order status based on Stripe events. The product.Store implements this.
// The return values are: (found, alreadyTerminal) where found=false means
// the order doesn't exist, and alreadyTerminal=true means the order was
// already in the target state (idempotent no-op).
type WebhookOrderUpdater interface {
	AtomicCompleteOrder(orderID string) (found bool, alreadyTerminal bool)
	AtomicMarkOrderFailed(orderID string) (found bool, alreadyTerminal bool)
	AtomicMarkOrderDisputed(orderID string) (found bool, alreadyTerminal bool)
	AtomicRefundOrder(orderID string) (found bool, alreadyTerminal bool)
}

// WebhookHandler handles Stripe webhook events and updates order status.
// It verifies the Stripe signature on each request.
//
// Supported events:
//   - checkout.session.completed → AtomicCompleteOrder
//   - checkout.session.expired    → AtomicMarkOrderFailed
//   - charge.failed               → AtomicMarkOrderFailed
//   - charge.dispute.created      → AtomicMarkOrderDisputed
//   - charge.refunded             → AtomicRefundOrder (if not already refunded)
type WebhookHandler struct {
	store  WebhookOrderUpdater
	secret string // Stripe webhook signing secret
}

// NewWebhookHandler creates a handler. The secret is read from
// STRIPE_WEBHOOK_SECRET if not provided directly.
func NewWebhookHandler(store WebhookOrderUpdater, secret string) *WebhookHandler {
	if secret == "" {
		secret = os.Getenv("STRIPE_WEBHOOK_SECRET")
	}
	return &WebhookHandler{store: store, secret: secret}
}

// ServeHTTP implements http.Handler. It reads the raw body, verifies the
// Stripe signature, parses the event, and dispatches to the store.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Verify Stripe signature if a secret is configured.
	if h.secret != "" {
		signature := r.Header.Get("Stripe-Signature")
		event, err := webhook.ConstructEventWithOptions(body, signature, h.secret, webhook.ConstructEventOptions{
			IgnoreAPIVersionMismatch: true,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("signature verification failed: %v", err), http.StatusBadRequest)
			return
		}
		h.handleEvent(w, &event)
		return
	}

	// No secret: parse without verification (for local dev/testing).
	var event stripe.Event
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "failed to parse event", http.StatusBadRequest)
		return
	}
	h.handleEvent(w, &event)
}

func (h *WebhookHandler) handleEvent(w http.ResponseWriter, event *stripe.Event) {
	switch event.Type {
	case "checkout.session.completed":
		h.dispatch(w, event, h.store.AtomicCompleteOrder)
	case "checkout.session.expired", "charge.failed":
		h.dispatch(w, event, h.store.AtomicMarkOrderFailed)
	case "charge.dispute.created":
		h.dispatch(w, event, h.store.AtomicMarkOrderDisputed)
	case "charge.refunded":
		h.dispatch(w, event, h.store.AtomicRefundOrder)
	default:
		// Unknown event: acknowledge but don't process.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ignored"}`))
	}
}

// dispatch extracts the order_id from the event metadata and calls the
// provided store method. The order_id is stored in client_reference_id
// or in the metadata.
func (h *WebhookHandler) dispatch(
	w http.ResponseWriter,
	event *stripe.Event,
	fn func(orderID string) (bool, bool),
) {
	orderID := extractOrderID(event)

	if orderID == "" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"no_order_id"}`))
		return
	}

	found, _ := fn(orderID)
	if !found {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"order_not_found"}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"processed"}`))
}

// extractOrderID pulls the order_id from the Stripe event's data object.
// Checks client_reference_id first (set on checkout sessions), then
// falls back to metadata.order_id.
func extractOrderID(event *stripe.Event) string {
	var dataObj map[string]interface{}
	if err := json.Unmarshal(event.Data.Raw, &dataObj); err != nil {
		return ""
	}

	// checkout.session events: client_reference_id
	if ref, ok := dataObj["client_reference_id"].(string); ok && ref != "" {
		return ref
	}

	// Fallback: metadata.order_id
	if meta, ok := dataObj["metadata"].(map[string]interface{}); ok {
		if oid, ok := meta["order_id"].(string); ok {
			return oid
		}
	}

	return ""
}

// IsWebhookConfigured returns true if STRIPE_WEBHOOK_SECRET is set.
func IsWebhookConfigured() bool {
	return os.Getenv("STRIPE_WEBHOOK_SECRET") != ""
}