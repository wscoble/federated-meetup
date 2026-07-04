// SPDX-License-Identifier: AGPL-3.0
package main

import (
	"context"
	"log"
	"time"

	"github.com/sscoble/federated-meetup/internal/email"
	"github.com/sscoble/federated-meetup/internal/web"
)

// startReminderScheduler runs a background loop that every 15 minutes
// checks for events starting in approximately 24 hours and sends
// reminder emails to confirmed RSVPs.
//
// The scheduler tracks which events have already had reminders sent
// (by event ID) to avoid duplicate emails across iterations.
func startReminderScheduler(store *web.Store, sender email.EmailSender, baseURL string) {
	reminderInterval := 15 * time.Minute
	ticker := time.NewTicker(reminderInterval)
	defer ticker.Stop()

	// Track events we've already sent reminders for.
	sent := make(map[string]bool)

	log.Printf("fedmeetup: reminder scheduler started (every %v)", reminderInterval)

	// Run once immediately, then on ticker.
	sendReminders(store, sender, baseURL, sent)

	for range ticker.C {
		sendReminders(store, sender, baseURL, sent)
	}
}

// sendReminders checks for events starting in ~24h and sends reminder
// emails to confirmed RSVPs.
func sendReminders(store *web.Store, sender email.EmailSender, baseURL string, sent map[string]bool) {
	// List upcoming events (up to 200).
	events, err := store.ListUpcomingEvents("", 200)
	if err != nil {
		log.Printf("reminder: list upcoming events: %v", err)
		return
	}

	now := time.Now().UTC()
	// Window: events starting between 22h and 26h from now.
	// This gives a wide enough window to not miss events across
	// 15-minute scheduler ticks.
	windowStart := now.Add(22 * time.Hour).Unix()
	windowEnd := now.Add(26 * time.Hour).Unix()

	for _, event := range events {
		if event.Cancelled {
			continue
		}
		if event.StartsAt < windowStart || event.StartsAt > windowEnd {
			continue
		}

		// Skip if we already sent a reminder for this event.
		eventKey := event.GroupKey + "/" + event.EventID
		if sent[eventKey] {
			continue
		}

		// Get confirmed RSVPs for this event.
		rsvps, err := store.ListRsvpsForEvent(event.GroupKey, event.EventID)
		if err != nil {
			log.Printf("reminder: list rsvps for %s: %v", eventKey, err)
			continue
		}

		if len(rsvps) == 0 {
			sent[eventKey] = true
			continue
		}

		// Get group name (best effort).
		groupName := event.GroupKey
		if g, err := store.GetGroup(event.GroupKey); err == nil && g.DisplayName != "" {
			groupName = g.DisplayName
		}

		eventURL := baseURL + "/events/" + event.GroupKey + "/" + event.EventID

		reminderData := email.EventReminderData{
			EventTitle:    event.Title,
			EventDate:     email.FormatEventDate(event.StartsAt),
			EventLocation: event.Location,
			GroupName:     groupName,
			EventURL:      eventURL,
		}

		subject, body, renderErr := email.RenderEventReminder(reminderData)
		if renderErr != nil {
			log.Printf("reminder: render for %s: %v", eventKey, renderErr)
			continue
		}

		sentCount := 0
		for _, rsvp := range rsvps {
			if err := sender.Send(context.Background(), rsvp.UserEmail, subject, body); err != nil {
				log.Printf("reminder: send to %s for %s: %v", rsvp.UserEmail, eventKey, err)
			} else {
				sentCount++
			}
		}

		if sentCount > 0 {
			log.Printf("reminder: sent %d reminder(s) for %s (%s)", sentCount, eventKey, event.Title)
		}

		// Mark as sent regardless of individual failures to avoid
		// retrying on every tick.
		sent[eventKey] = true
	}
}