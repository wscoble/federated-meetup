// SPDX-License-Identifier: AGPL-3.0

// Package activitypub provides ActivityPub federation for federated-meetup
// groups. Each group is exposed as an ActivityPub actor with:
//
//   - Actor document at /ap/actor/{group_name}
//   - Outbox at /ap/outbox/{group_name} (Create/Event activities)
//   - Inbox at /ap/inbox/{group_name} (Follow, Undo Follow)
//   - WebFinger at /.well-known/webfinger?resource=acct:{group_name}@{host}
//
// The implementation is read-only for v0: it publishes events as
// Create/Event activities and accepts Follow requests (recording
// followers for future delivery). It does not yet deliver activities
// to remote inboxes (that requires a delivery worker, planned for v1).
//
// This satisfies the v0.4 spec requirement: "ActivityPub publishing in v0".
package activitypub

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/product/v1"
)

// ActivityPubService provides ActivityPub endpoints for the web server.
// It reads group and event data from the product service's store.
type ActivityPubService struct {
	baseURL   string // e.g. "https://fm.scoble.me"
	product   ProductStore
	followers map[string][]string // group_name → []follower actor URLs
}

// ProductStore is the interface ActivityPub needs from the product layer.
type ProductStore interface {
	GetGroup(canonicalName string) (*pb.Group, bool)
	ListEventsByGroup(groupID string) []*pb.Event
}

// NewActivityPubService creates a new ActivityPubService.
func NewActivityPubService(baseURL string, store ProductStore) *ActivityPubService {
	return &ActivityPubService{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		product:   store,
		followers: make(map[string][]string),
	}
}

// RegisterRoutes registers ActivityPub routes on the given mux.
func (a *ActivityPubService) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/webfinger", a.handleWebFinger)
	mux.HandleFunc("/ap/actor/{name}", a.handleActor)
	mux.HandleFunc("/ap/outbox/{name}", a.handleOutbox)
	mux.HandleFunc("/ap/inbox/{name}", a.handleInbox)
}

// ---- Types ----

// ActivityPubActor is the AP actor document for a group.
type ActivityPubActor struct {
	Context           []string `json:"@context"`
	Type              string   `json:"type"`
	ID                string   `json:"id"`
	Inbox             string   `json:"inbox"`
	Outbox            string   `json:"outbox"`
	Following         string   `json:"following"`
	Followers         string   `json:"followers"`
	PreferredUsername string   `json:"preferredUsername"`
	Name              string   `json:"name"`
	Summary           string   `json:"summary"`
	URL               string   `json:"url"`
}

// ActivityPubActivity is a generic AP activity.
type ActivityPubActivity struct {
	Context string      `json:"@context"`
	Type    string      `json:"type"`
	ID      string      `json:"id"`
	Actor   string      `json:"actor"`
	Object  interface{} `json:"object"`
	To      []string    `json:"to,omitempty"`
	CC      []string    `json:"cc,omitempty"`
}

// OrderedCollection is an AP ordered collection (used for outbox/inbox).
type OrderedCollection struct {
	Context      string        `json:"@context"`
	Type         string        `json:"type"`
	ID           string        `json:"id"`
	TotalItems   int           `json:"totalItems"`
	OrderedItems []interface{} `json:"orderedItems"`
}

// APEvent is an ActivityPub Event object.
type APEvent struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime,omitempty"`
	Location    string `json:"location,omitempty"`
	URL         string `json:"url"`
	AttributedTo string `json:"attributedTo"`
}

// ---- Handlers ----

func (a *ActivityPubService) handleWebFinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		http.Error(w, "missing resource", http.StatusBadRequest)
		return
	}

	// Parse acct:group_name@host
	if !strings.HasPrefix(resource, "acct:") {
		http.Error(w, "unsupported resource type", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(resource, "acct:"), "@", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid acct format", http.StatusBadRequest)
		return
	}
	groupName := parts[0]

	// Verify group exists
	if _, ok := a.product.GetGroup(groupName); !ok {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	actorURL := fmt.Sprintf("%s/ap/actor/%s", a.baseURL, groupName)
	profileURL := fmt.Sprintf("%s/groups/%s", a.baseURL, groupName)

	resp := map[string]interface{}{
		"subject": resource,
		"links": []map[string]string{
			{
				"rel":  "self",
				"type": "application/activity+json",
				"href": actorURL,
			},
			{
				"rel":  "http://webfinger.net/rel/profile-page",
				"type": "text/html",
				"href": profileURL,
			},
		},
	}

	writeJSON(w, resp)
}

func (a *ActivityPubService) handleActor(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	group, ok := a.product.GetGroup(name)
	if !ok {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	actor := ActivityPubActor{
		Context: []string{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
		},
		Type:              "Group",
		ID:                fmt.Sprintf("%s/ap/actor/%s", a.baseURL, name),
		Inbox:             fmt.Sprintf("%s/ap/inbox/%s", a.baseURL, name),
		Outbox:            fmt.Sprintf("%s/ap/outbox/%s", a.baseURL, name),
		Following:         fmt.Sprintf("%s/ap/actor/%s/following", a.baseURL, name),
		Followers:         fmt.Sprintf("%s/ap/actor/%s/followers", a.baseURL, name),
		PreferredUsername: name,
		Name:              group.DisplayName,
		Summary:           group.Description,
		URL:               fmt.Sprintf("%s/groups/%s", a.baseURL, name),
	}

	writeActivityJSON(w, actor)
}

func (a *ActivityPubService) handleOutbox(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	group, ok := a.product.GetGroup(name)
	if !ok {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	events := a.product.ListEventsByGroup(group.GroupId)
	actorURL := fmt.Sprintf("%s/ap/actor/%s", a.baseURL, name)

	items := make([]interface{}, 0, len(events))
	for _, e := range events {
		eventURL := fmt.Sprintf("%s/events/%s/%s", a.baseURL, name, e.EventId)
		activity := ActivityPubActivity{
			Context: "https://www.w3.org/ns/activitystreams",
			Type:    "Create",
			ID:      fmt.Sprintf("%s/activities/create-%s", actorURL, e.EventId),
			Actor:   actorURL,
			Object: APEvent{
				Type:         "Event",
				ID:           eventURL,
				Name:         e.Title,
				Summary:      e.Description,
				StartTime:    e.StartsAt.AsTime().UTC().Format(time.RFC3339),
				Location:      e.Location,
				URL:          eventURL,
				AttributedTo: actorURL,
			},
			To: []string{"https://www.w3.org/ns/activitystreams#Public"},
		}
		items = append(items, activity)
	}

	collection := OrderedCollection{
		Context:      "https://www.w3.org/ns/activitystreams",
		Type:         "OrderedCollection",
		ID:           fmt.Sprintf("%s/ap/outbox/%s", a.baseURL, name),
		TotalItems:   len(items),
		OrderedItems: items,
	}

	writeActivityJSON(w, collection)
}

func (a *ActivityPubService) handleInbox(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	// Verify group exists
	if _, ok := a.product.GetGroup(name); !ok {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodGet {
		// Return empty inbox
		collection := OrderedCollection{
			Context:    "https://www.w3.org/ns/activitystreams",
			Type:       "OrderedCollection",
			ID:         fmt.Sprintf("%s/ap/inbox/%s", a.baseURL, name),
			TotalItems: 0,
		}
		writeActivityJSON(w, collection)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse incoming activity
	var activity map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&activity); err != nil {
		http.Error(w, "failed to parse activity", http.StatusBadRequest)
		return
	}

	activityType, _ := activity["type"].(string)
	switch activityType {
	case "Follow":
		actor, _ := activity["actor"].(string)
		if actor != "" {
			a.followers[name] = append(a.followers[name], actor)
			log.Printf("activitypub: group %s has new follower: %s (total: %d)", name, actor, len(a.followers[name]))
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted"}`))
	case "Undo":
		// Parse Undo of Follow
		obj, _ := activity["object"].(map[string]interface{})
		if obj != nil {
			if undoType, _ := obj["type"].(string); undoType == "Follow" {
				actor, _ := activity["actor"].(string)
				a.removeFollower(name, actor)
				log.Printf("activitypub: group %s: unfollowed by %s", name, actor)
			}
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted"}`))
	default:
		// Unknown activity: acknowledge
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"ignored"}`))
	}
}

// Followers returns the list of follower actor URLs for a group.
func (a *ActivityPubService) Followers(groupName string) []string {
	return a.followers[groupName]
}

// removeFollower removes a follower from the list.
func (a *ActivityPubService) removeFollower(name, actor string) {
	followers := a.followers[name]
	for i, f := range followers {
		if f == actor {
			a.followers[name] = append(followers[:i], followers[i+1:]...)
			return
		}
	}
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

func writeActivityJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/activity+json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}