// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"github.com/wscoble/federated-meetup/internal/payment"
)

// webhookAdapter wraps Store to satisfy payment.WebhookOrderUpdater.
// The Store's atomic methods return (*pb.Order, bool, bool) but the
// webhook interface expects (bool, bool). This adapter drops the order.
type webhookAdapter struct{ store *Store }

func (a webhookAdapter) AtomicCompleteOrder(orderID string) (bool, bool) {
	_, found, already := a.store.AtomicCompleteOrder(orderID)
	return found, already
}
func (a webhookAdapter) AtomicMarkOrderFailed(orderID string) (bool, bool) {
	_, found, already := a.store.AtomicMarkOrderFailed(orderID)
	return found, already
}
func (a webhookAdapter) AtomicMarkOrderDisputed(orderID string) (bool, bool) {
	_, found, already := a.store.AtomicMarkOrderDisputed(orderID)
	return found, already
}
func (a webhookAdapter) AtomicRefundOrder(orderID string) (bool, bool) {
	_, found, already := a.store.AtomicRefundOrder(orderID, 0) // webhook refunds are always full
	return found, already
}

// WebhookHandler returns a payment.WebhookHandler backed by this store.
func (s *Store) WebhookHandler(secret string) *payment.WebhookHandler {
	return payment.NewWebhookHandler(webhookAdapter{store: s}, secret)
}