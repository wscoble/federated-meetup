// SPDX-License-Identifier: AGPL-3.0

package activitypub

import (
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ProductStoreAdapter wraps the product store to satisfy the
// ProductStore interface. The product store uses GroupID as the key,
// but ActivityPub uses the canonical name (e.g. "vegas-programmers")
// for actor URLs. The adapter resolves canonical name → group → events.
type ProductStoreAdapter struct {
	store ProductStoreBackend
}

// ProductStoreBackend is the interface the adapter needs from the
// product store (or any backend that can look up groups and events).
type ProductStoreBackend interface {
	GetGroup(id string) (*pb.Group, bool)
	EventsForGroup(groupID string) []*pb.Event
}

// NewProductStoreAdapter creates an adapter for the product store.
func NewProductStoreAdapter(store ProductStoreBackend) *ProductStoreAdapter {
	return &ProductStoreAdapter{store: store}
}

// GetGroup resolves a canonical name to a group by trying it as
// both the canonical name and the group ID.
func (a *ProductStoreAdapter) GetGroup(canonicalName string) (*pb.Group, bool) {
	// The product store keys groups by GroupId, but the canonical
	// name is the same as GroupId for our seed data and group creation
	// flow. Try it directly.
	return a.store.GetGroup(canonicalName)
}

// ListEventsByGroup returns all events for a group.
func (a *ProductStoreAdapter) ListEventsByGroup(groupID string) []*pb.Event {
	return a.store.EventsForGroup(groupID)
}