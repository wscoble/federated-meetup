// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/wscoble/federated-meetup/internal/email"
	"github.com/wscoble/federated-meetup/internal/notify"
)

// notifyEventCreation enqueues notifications to all confirmed RSVPs for
// a group when a new event is created. It respects group notification
// preferences (notify_on_new_event) and quiet hours.
func (s *Server) notifyEventCreation(groupKey, eventID, eventTitle string, startsAt int64, location string) {
	if s.notifier == nil || s.notifyQ == nil || s.prefs == nil {
		return // notification system not configured
	}

	// Check group prefs.
	groupPrefs, err := s.prefs.GetGroupPrefs(groupKey)
	if err != nil {
		log.Printf("notify: get group prefs for %s: %v", groupKey, err)
		return
	}
	if !groupPrefs.NotifyOnNewEvent {
		return // group has new-event notifications disabled
	}

	// Get the group name for the notification.
	groupName := groupKey
	if g, err := s.store.GetGroup(groupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	// Get all users who have RSVPs for events in this group (best-effort:
	// we notify everyone who has RSVP'd to any event in the group).
	// In a real system, this would be "group members" — for now, we use
	// confirmed RSVPs across all events in the group.
	events, err := s.store.ListEventsByGroup(groupKey)
	if err != nil {
		log.Printf("notify: list events for group %s: %v", groupKey, err)
		return
	}

	// Collect unique emails from all confirmed RSVPs.
	notified := make(map[string]bool)
	for _, e := range events {
		rsvps, err := s.store.ListRsvpsForEvent(e.GroupKey, e.EventID)
		if err != nil {
			continue
		}
		for _, rsvp := range rsvps {
			if notified[rsvp.UserEmail] {
				continue
			}
			notified[rsvp.UserEmail] = true

			// Check quiet hours for the group.
			scheduledAt := time.Now().Unix()
			if groupPrefs.QuietHoursStart != "" && groupPrefs.QuietHoursEnd != "" {
				qh := notify.QuietHours{
					Start: groupPrefs.QuietHoursStart,
					End:   groupPrefs.QuietHoursEnd,
				}
				delay := notify.ShouldDeliverNow(qh, s.now())
				if delay > 0 {
					scheduledAt = s.now().Add(delay).Unix()
				}
			}

			// Build the notification body.
			eventURL := s.baseURL + "/events/" + groupKey + "/" + eventID
			subject, body, renderErr := email.RenderEventReminder(email.EventReminderData{
				EventTitle:    eventTitle,
				EventDate:     email.FormatEventDate(startsAt),
				EventLocation: location,
				GroupName:     groupName,
				EventURL:      eventURL,
			})
			if renderErr != nil {
				log.Printf("notify: render event reminder for %s: %v", rsvp.UserEmail, renderErr)
				continue
			}

			// Enqueue for background processing.
			_, err := s.notifyQ.Enqueue(rsvp.UserEmail, "auto", subject, body, scheduledAt)
			if err != nil {
				log.Printf("notify: enqueue for %s: %v", rsvp.UserEmail, err)
			}
		}
	}

	if len(notified) > 0 {
		log.Printf("notify: enqueued %d notification(s) for new event %s/%s", len(notified), groupKey, eventID)
	}
}

// notifyRsvpConfirmation sends an RSVP confirmation notification to the
// user who just confirmed their RSVP. Uses the best available channel
// (push → email → SMS) based on user preferences.
func (s *Server) notifyRsvpConfirmation(userEmail, eventTitle string, eventStartsAt int64, eventLocation, groupName, cancelURL string) {
	if s.notifier == nil || s.notifyQ == nil {
		return
	}

	// Render the notification.
	subject, body, err := email.RenderRsvpConfirmed(email.RsvpConfirmedData{
		EventTitle:    eventTitle,
		EventDate:     email.FormatEventDate(eventStartsAt),
		EventLocation: eventLocation,
		GroupName:     groupName,
		CancelURL:     cancelURL,
	})
	if err != nil {
		log.Printf("notify: render rsvp confirmation: %v", err)
		return
	}

	// Enqueue for background processing.
	_, err = s.notifyQ.EnqueueNow(userEmail, "auto", subject, body)
	if err != nil {
		log.Printf("notify: enqueue rsvp confirmation for %s: %v", userEmail, err)
	}
}

// notifyScheduleChange sends a schedule-change notification to all
// confirmed RSVPs when an event is cancelled or rescheduled.
func (s *Server) notifyScheduleChange(groupKey, eventID, eventTitle string, startsAt int64, location, changeSummary string) {
	if s.notifier == nil || s.notifyQ == nil || s.prefs == nil {
		return
	}

	// Check group prefs.
	groupPrefs, err := s.prefs.GetGroupPrefs(groupKey)
	if err != nil {
		log.Printf("notify: get group prefs for %s: %v", groupKey, err)
		return
	}
	// Note: schedule changes are always sent (even if NotifyOnNewEvent is
	// false) because they're important. But we still respect quiet hours.

	// Get group name.
	groupName := groupKey
	if g, err := s.store.GetGroup(groupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	// Get all confirmed RSVPs for this event.
	rsvps, err := s.store.ListRsvpsForEvent(groupKey, eventID)
	if err != nil {
		log.Printf("notify: list rsvps for %s/%s: %v", groupKey, eventID, err)
		return
	}

	eventURL := s.baseURL + "/events/" + groupKey + "/" + eventID

	for _, rsvp := range rsvps {
		// Check quiet hours.
		scheduledAt := time.Now().Unix()
		if groupPrefs.QuietHoursStart != "" && groupPrefs.QuietHoursEnd != "" {
			qh := notify.QuietHours{
				Start: groupPrefs.QuietHoursStart,
				End:   groupPrefs.QuietHoursEnd,
			}
			delay := notify.ShouldDeliverNow(qh, s.now())
			if delay > 0 {
				scheduledAt = s.now().Add(delay).Unix()
			}
		}

		subject, body, renderErr := email.RenderScheduleChange(email.ScheduleChangeData{
			EventTitle:    eventTitle,
			EventDate:      email.FormatEventDate(startsAt),
			EventLocation:  location,
			GroupName:      groupName,
			EventURL:       eventURL,
			ChangeSummary:  changeSummary,
		})
		if renderErr != nil {
			log.Printf("notify: render schedule change: %v", renderErr)
			continue
		}

		_, err := s.notifyQ.Enqueue(rsvp.UserEmail, "auto", subject, body, scheduledAt)
		if err != nil {
			log.Printf("notify: enqueue schedule change for %s: %v", rsvp.UserEmail, err)
		}
	}

	if len(rsvps) > 0 {
		log.Printf("notify: enqueued %d schedule-change notification(s) for %s/%s", len(rsvps), groupKey, eventID)
	}
}

// notifyNewMessage sends a new-message notification to group members.
// This is a stub for when the messaging feature is built.
func (s *Server) notifyNewMessage(groupKey, senderName, messagePreview, messageURL string) {
	if s.notifier == nil || s.notifyQ == nil || s.prefs == nil {
		return
	}

	groupPrefs, err := s.prefs.GetGroupPrefs(groupKey)
	if err != nil {
		return
	}
	if !groupPrefs.NotifyOnNewMessage {
		return
	}

	groupName := groupKey
	if g, err := s.store.GetGroup(groupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	subject, body, err := email.RenderNewMessage(email.NewMessageData{
		SenderName:     senderName,
		GroupName:      groupName,
		MessagePreview: messagePreview,
		MessageURL:     messageURL,
	})
	if err != nil {
		log.Printf("notify: render new message: %v", err)
		return
	}

	// In a full implementation, we'd iterate over group members.
	// For now, this is a stub that logs the intent.
	log.Printf("notify: new message in %s from %s — would notify group members", groupName, senderName)
	_ = subject
	_ = body
	_ = context.Background()
	_ = fmt.Sprintf
}

// resolveUserForNotify looks up a user's notification profile for the
// queue processor. It checks user preferences and looks up push
// subscriptions.
func (s *Server) resolveUserForNotify(ctx context.Context, email string) (notify.UserProfile, error) {
	profile := notify.UserProfile{
		Email: email,
	}

	// Get user prefs (defaults to email=true if not set).
	if s.prefs != nil {
		prefs, err := s.prefs.GetUserPrefs(email)
		if err != nil {
			return profile, err
		}
		profile.NotifyEmail = prefs.NotifyEmail
		profile.NotifySMS = prefs.NotifySMS
		profile.NotifyPush = prefs.NotifyPush
	} else {
		profile.NotifyEmail = true
	}

	// Get push subscriptions.
	if s.pushMgr != nil && profile.NotifyPush {
		subs, err := s.pushMgr.GetSubscriptionsForUser(email)
		if err == nil && len(subs) > 0 {
			// Use the first subscription. In a full implementation,
			// we'd try each subscription.
			sub := subs[0]
			profile.PushSubscription = fmt.Sprintf(
				`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`,
				sub.Endpoint, sub.P256dh, sub.Auth,
			)
		}
	}

	return profile, nil
}