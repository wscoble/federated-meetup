// SPDX-License-Identifier: AGPL-3.0

package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockOrderUpdater is a test implementation of WebhookOrderUpdater.
type mockOrderUpdater struct {
	completed  map[string]bool
	failed     map[string]bool
	disputed   map[string]bool
	refunded   map[string]bool
}

func newMockOrderUpdater() *mockOrderUpdater {
	return &mockOrderUpdater{
		completed: make(map[string]bool),
		failed:    make(map[string]bool),
		disputed:  make(map[string]bool),
		refunded:  make(map[string]bool),
	}
}

func (m *mockOrderUpdater) AtomicCompleteOrder(orderID string) (bool, bool) {
	if m.completed[orderID] {
		return true, true
	}
	m.completed[orderID] = true
	return true, false
}
func (m *mockOrderUpdater) AtomicMarkOrderFailed(orderID string) (bool, bool) {
	if m.failed[orderID] {
		return true, true
	}
	m.failed[orderID] = true
	return true, false
}
func (m *mockOrderUpdater) AtomicMarkOrderDisputed(orderID string) (bool, bool) {
	if m.disputed[orderID] {
		return true, true
	}
	m.disputed[orderID] = true
	return true, false
}
func (m *mockOrderUpdater) AtomicRefundOrder(orderID string) (bool, bool) {
	if m.refunded[orderID] {
		return true, true
	}
	m.refunded[orderID] = true
	return true, false
}

func makeStripeEvent(eventType string, orderID string) []byte {
	// Build the data object with client_reference_id and metadata.
	// stripe.EventData.Raw is tagged json:"object", so the path is data.object.
	dataObject, _ := json.Marshal(map[string]interface{}{
		"client_reference_id": orderID,
		"metadata": map[string]string{
			"order_id": orderID,
		},
	})

	event := map[string]interface{}{
		"type": eventType,
		"data": map[string]interface{}{
			"object": json.RawMessage(dataObject),
		},
	}
	b, _ := json.Marshal(event)
	return b
}

func TestWebhook_CheckoutCompleted(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "") // no secret = skip verification

	body := makeStripeEvent("checkout.session.completed", "ord-123")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !updater.completed["ord-123"] {
		t.Error("order should have been marked completed")
	}
}

func TestWebhook_ChargeFailed(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	body := makeStripeEvent("charge.failed", "ord-456")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !updater.failed["ord-456"] {
		t.Error("order should have been marked failed")
	}
}

func TestWebhook_DisputeCreated(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	body := makeStripeEvent("charge.dispute.created", "ord-789")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !updater.disputed["ord-789"] {
		t.Error("order should have been marked disputed")
	}
}

func TestWebhook_ChargeRefunded(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	body := makeStripeEvent("charge.refunded", "ord-refund")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !updater.refunded["ord-refund"] {
		t.Error("order should have been refunded")
	}
}

func TestWebhook_IdempotentComplete(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	body := makeStripeEvent("checkout.session.completed", "ord-idem")
	req1 := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	// Both should succeed (idempotent).
	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Errorf("expected both 200, got %d and %d", w1.Code, w2.Code)
	}
}

func TestWebhook_UnknownEvent(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	body := makeStripeEvent("customer.created", "ord-xyz")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Nothing should have been updated.
	if len(updater.completed) > 0 || len(updater.failed) > 0 {
		t.Error("unknown event should not trigger any updates")
	}
}

func TestWebhook_NoOrderID(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	// Event with no client_reference_id or metadata.order_id.
	emptyObj, _ := json.Marshal(map[string]interface{}{})
	event := map[string]interface{}{
		"type": "checkout.session.completed",
		"data": map[string]interface{}{
			"object": json.RawMessage(emptyObj),
		},
	}
	body, _ := json.Marshal(event)

	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if len(updater.completed) > 0 {
		t.Error("no order should have been completed")
	}
}

func TestWebhook_MethodNotAllowed(t *testing.T) {
	updater := newMockOrderUpdater()
	h := NewWebhookHandler(updater, "")

	req := httptest.NewRequest(http.MethodGet, "/stripe/webhook", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// Ensure mockOrderUpdater also satisfies the interface at compile time.
var _ WebhookOrderUpdater = (*mockOrderUpdater)(nil)

// Ensure MockProvider still works (regression).
func TestPaymentProvider_MockStillWorks(t *testing.T) {
	p := NewMockProvider()
	_, _, err := p.CreateCheckoutSession(context.Background(), CheckoutParams{
		OrderID: "test",
	})
	if err != nil {
		t.Fatalf("mock provider failed: %v", err)
	}
}