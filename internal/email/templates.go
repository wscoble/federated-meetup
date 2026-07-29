// SPDX-License-Identifier: AGPL-3.0
package email

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"
)

// ---- Template data structs ----

// RsvpConfirmData is the data for the RSVP confirmation email (magic link).
type RsvpConfirmData struct {
	EventTitle string
	EventDate  string // human-readable, e.g. "Mon, Jul 15, 2026 6:00 PM"
	EventLocation string
	MagicLink  string // absolute URL
	GroupName  string
}

// RsvpConfirmedData is the data for the RSVP-confirmed email.
type RsvpConfirmedData struct {
	EventTitle    string
	EventDate     string
	EventLocation string
	GroupName     string
	CancelURL     string // absolute URL to cancel RSVP
}

// EventReminderData is the data for the 24h-before reminder email.
type EventReminderData struct {
	EventTitle    string
	EventDate     string
	EventLocation string
	GroupName     string
	EventURL      string // absolute URL to the event page
}

// RsvpReminderData is the data for the RSVP follow-up reminder email
// sent to members who haven't RSVP'd yet (default 48h before event).
type RsvpReminderData struct {
	EventTitle    string
	EventDate     string
	EventLocation string
	GroupName     string
	EventURL      string // absolute URL to the event page
}

// OrganizerNotifyData is the data for the organizer notification email.
type OrganizerNotifyData struct {
	EventTitle string
	Name      string
	Email     string
	EventDate string
}

// MyRsvpsLinkData is the data for the /my-rsvps magic-link email.
//
// SECURITY: the body MUST be content-free. The only recipient-specific
// content allowed is the recipient's email (already in the To: header)
// and the magic link. No event names, no group names, no RSVP details
// — a third party intercepting the email must not learn what the
// recipient has RSVPed to. See SECURITY.md "Email enumeration /
// harassment" (surface #3) and the anti-dox skill Check 7.
type MyRsvpsLinkData struct {
	MagicLink string // absolute URL
	ExpiresIn string // human-readable TTL, e.g. "24 hours"
}

// ---- Template definitions (plain text) ----

const rsvpConfirmTpl = `Hi,

You have been invited to RSVP for "{{.EventTitle}}".

When: {{.EventDate}}
Where: {{.EventLocation}}
Group: {{.GroupName}}

Click the link below to confirm your RSVP:

  {{.MagicLink}}

If you did not request this RSVP, you can safely ignore this email.

— Federated Meetup
`

const rsvpConfirmedTpl = `Hi,

Your RSVP is confirmed for "{{.EventTitle}}".

When: {{.EventDate}}
Where: {{.EventLocation}}
Group: {{.GroupName}}

We look forward to seeing you there!

If you need to cancel, visit:
  {{.CancelURL}}

— Federated Meetup
`

const eventReminderTpl = `Hi,

This is a reminder that "{{.EventTitle}}" is happening tomorrow.

When: {{.EventDate}}
Where: {{.EventLocation}}
Group: {{.GroupName}}

Event page: {{.EventURL}}

See you there!

— Federated Meetup
`

const rsvpReminderTpl = `Hi,

This is a friendly reminder that "{{.EventTitle}}" is coming up soon
and we haven't seen your RSVP yet.

When: {{.EventDate}}
Where: {{.EventLocation}}
Group: {{.GroupName}}

Let us know if you can make it — visit the event page to RSVP:

  {{.EventURL}}

— Federated Meetup
`

const organizerNotifyTpl = `Hello,

A new RSVP has been received for "{{.EventTitle}}".

Name: {{.Name}}
Email: {{.Email}}
Event date: {{.EventDate}}

— Federated Meetup
`

// myRsvpsLinkTpl is the /my-rsvps magic-link delivery email.
//
// Content-free by design. The recipient's email is in the To: header
// and the magic link is the only thing gated by the email; the body
// itself says nothing about what the recipient has RSVPed to. This
// is Check 7 of the anti-dox skill: the side channel must not be
// a data leak.
const myRsvpsLinkTpl = `Hi,

Someone (hopefully you) requested a sign-in link to view their RSVPs
on Federated Meetup.

If that was you, click the link below to view your RSVPs. The link
will expire in {{.ExpiresIn}} and can be used only once.

  {{.MagicLink}}

If you did not request this, you can safely ignore this email —
nothing was changed, and your account (if any) is unaffected.

— Federated Meetup
`

// ---- Render functions ----

var (
	tplRsvpConfirm     = template.Must(template.New("rsvp_confirm").Parse(rsvpConfirmTpl))
	tplRsvpConfirmed   = template.Must(template.New("rsvp_confirmed").Parse(rsvpConfirmedTpl))
	tplEventReminder   = template.Must(template.New("event_reminder").Parse(eventReminderTpl))
	tplRsvpReminder    = template.Must(template.New("rsvp_reminder").Parse(rsvpReminderTpl))
	tplOrganizerNotify = template.Must(template.New("organizer_notify").Parse(organizerNotifyTpl))
	tplMyRsvpsLink     = template.Must(template.New("my_rsvps_link").Parse(myRsvpsLinkTpl))
)

// RenderRsvpConfirm renders the RSVP confirmation (magic-link) email body.
func RenderRsvpConfirm(data RsvpConfirmData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplRsvpConfirm.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render rsvp_confirm: %w", err)
	}
	// PRIVACY §4d: no event names in subject lines — content-free where possible.
	return "Confirm your RSVP on Federated Meetup", buf.String(), nil
}

// RenderRsvpConfirmed renders the RSVP-confirmed email body.
func RenderRsvpConfirmed(data RsvpConfirmedData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplRsvpConfirmed.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render rsvp_confirmed: %w", err)
	}
	// PRIVACY §4d: no event names in subject lines — content-free where possible.
	return "Your RSVP is confirmed on Federated Meetup", buf.String(), nil
}

// RenderEventReminder renders the 24h-before reminder email body.
func RenderEventReminder(data EventReminderData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplEventReminder.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render event_reminder: %w", err)
	}
	// PRIVACY §4d: no event names in subject lines — content-free where possible.
	return "Reminder: your event is tomorrow on Federated Meetup", buf.String(), nil
}

// RenderRsvpReminder renders the RSVP follow-up reminder email body
// (sent to members who haven't RSVP'd, default 48h before event).
func RenderRsvpReminder(data RsvpReminderData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplRsvpReminder.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render rsvp_reminder: %w", err)
	}
	return "Reminder: RSVP soon for your event on Federated Meetup", buf.String(), nil
}

// RenderOrganizerNotify renders the organizer notification email body.
func RenderOrganizerNotify(data OrganizerNotifyData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplOrganizerNotify.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render organizer_notify: %w", err)
	}
	return fmt.Sprintf("New RSVP for %s from %s", data.EventTitle, data.Name), buf.String(), nil
}

// RenderMyRsvpsLink renders the /my-rsvps magic-link delivery email.
// The body is content-free (no RSVP/event/group data) — see the
// security note on MyRsvpsLinkData above.
func RenderMyRsvpsLink(data MyRsvpsLinkData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplMyRsvpsLink.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render my_rsvps_link: %w", err)
	}
	return "View your RSVPs on Federated Meetup", buf.String(), nil
}

// ---- Notification templates (plaintext) ----

// NewMessageData is the data for the new-message notification.
type NewMessageData struct {
	SenderName  string
	GroupName   string
	MessagePreview string // first ~100 chars of the message
	MessageURL  string // absolute URL to view the full message
}

// ScheduleChangeData is the data for the schedule-change notification.
type ScheduleChangeData struct {
	EventTitle    string
	EventDate     string // human-readable new date
	EventLocation string
	GroupName     string
	EventURL      string
	ChangeSummary string // human-readable summary, e.g. "Time changed from 6 PM to 7 PM"
}

const newMessageTpl = `Hi,

{{.SenderName}} posted a new message in {{.GroupName}}:

  {{.MessagePreview}}

Read the full message:
  {{.MessageURL}}

— Federated Meetup
`

const scheduleChangeTpl = `Hi,

The event "{{.EventTitle}}" in {{.GroupName}} has been updated.

  {{.ChangeSummary}}

New details:
  When: {{.EventDate}}
  Where: {{.EventLocation}}

Event page: {{.EventURL}}

— Federated Meetup
`

var (
	tplNewMessage    = template.Must(template.New("new_message").Parse(newMessageTpl))
	tplScheduleChange = template.Must(template.New("schedule_change").Parse(scheduleChangeTpl))
)

// RenderNewMessage renders the new-message notification email body.
func RenderNewMessage(data NewMessageData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplNewMessage.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render new_message: %w", err)
	}
	return "New message in your group on Federated Meetup", buf.String(), nil
}

// RenderScheduleChange renders the schedule-change notification email body.
func RenderScheduleChange(data ScheduleChangeData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplScheduleChange.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render schedule_change: %w", err)
	}
	return "Event updated on Federated Meetup", buf.String(), nil
}

// ---- Notification templates (HTML) ----

// RenderEventReminderHTML renders an HTML version of the event reminder.
func RenderEventReminderHTML(data EventReminderData) (subject, html string, err error) {
	subject, _, err = RenderEventReminder(data)
	if err != nil {
		return "", "", err
	}
	html = renderHTMLEmail("Event Reminder", data.EventTitle, data.EventDate, data.EventLocation, data.GroupName, data.EventURL)
	return subject, html, nil
}

// RenderRsvpConfirmationHTML renders an HTML version of the RSVP confirmation.
func RenderRsvpConfirmationHTML(data RsvpConfirmedData) (subject, html string, err error) {
	subject, _, err = RenderRsvpConfirmed(data)
	if err != nil {
		return "", "", err
	}
	html = renderHTMLEmail("RSVP Confirmed", data.EventTitle, data.EventDate, data.EventLocation, data.GroupName, data.CancelURL)
	return subject, html, nil
}

// RenderNewMessageHTML renders an HTML version of the new-message notification.
func RenderNewMessageHTML(data NewMessageData) (subject, html string, err error) {
	subject, _, err = RenderNewMessage(data)
	if err != nil {
		return "", "", err
	}
	html = renderHTMLEmailMessage(data.SenderName, data.GroupName, data.MessagePreview, data.MessageURL)
	return subject, html, nil
}

// RenderScheduleChangeHTML renders an HTML version of the schedule-change notification.
func RenderScheduleChangeHTML(data ScheduleChangeData) (subject, html string, err error) {
	subject, _, err = RenderScheduleChange(data)
	if err != nil {
		return "", "", err
	}
	html = renderHTMLEmailChange(data.EventTitle, data.EventDate, data.EventLocation, data.GroupName, data.EventURL, data.ChangeSummary)
	return subject, html, nil
}

// renderHTMLEmail produces a basic HTML email body for event-style notifications.
func renderHTMLEmail(label, eventTitle, eventDate, eventLocation, groupName, url string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #2563eb;">%s</h2>
<p style="font-size: 16px; color: #1f2937;">%s</p>
<table style="font-size: 15px; color: #374151; margin: 16px 0;">
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">When:</td><td>%s</td></tr>
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">Where:</td><td>%s</td></tr>
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">Group:</td><td>%s</td></tr>
</table>
<p><a href="%s" style="display: inline-block; padding: 10px 24px; background-color: #2563eb; color: #fff; text-decoration: none; border-radius: 6px;">View Event</a></p>
<hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;">
<p style="font-size: 13px; color: #6b7280;">— Federated Meetup</p>
</body>
</html>`, label, label, eventTitle, eventDate, eventLocation, groupName, url)
}

// renderHTMLEmailMessage produces an HTML email for new-message notifications.
func renderHTMLEmailMessage(senderName, groupName, preview, url string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>New Message</title></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #2563eb;">New Message</h2>
<p style="font-size: 16px; color: #1f2937;"><strong>%s</strong> posted in <strong>%s</strong>:</p>
<blockquote style="border-left: 3px solid #e5e7eb; padding-left: 16px; margin: 16px 0; color: #4b5563; font-size: 15px;">%s</blockquote>
<p><a href="%s" style="display: inline-block; padding: 10px 24px; background-color: #2563eb; color: #fff; text-decoration: none; border-radius: 6px;">Read Full Message</a></p>
<hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;">
<p style="font-size: 13px; color: #6b7280;">— Federated Meetup</p>
</body>
</html>`, senderName, groupName, preview, url)
}

// renderHTMLEmailChange produces an HTML email for schedule-change notifications.
func renderHTMLEmailChange(eventTitle, eventDate, eventLocation, groupName, url, changeSummary string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Event Updated</title></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Event Updated</h2>
<p style="font-size: 16px; color: #1f2937;">%s</p>
<div style="background-color: #fef3c7; padding: 12px 16px; border-radius: 6px; margin: 16px 0; font-size: 15px; color: #78350f;">%s</div>
<table style="font-size: 15px; color: #374151; margin: 16px 0;">
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">When:</td><td>%s</td></tr>
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">Where:</td><td>%s</td></tr>
<tr><td style="padding: 4px 12px 4px 0; font-weight: 600;">Group:</td><td>%s</td></tr>
</table>
<p><a href="%s" style="display: inline-block; padding: 10px 24px; background-color: #2563eb; color: #fff; text-decoration: none; border-radius: 6px;">View Event</a></p>
<hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;">
<p style="font-size: 13px; color: #6b7280;">— Federated Meetup</p>
</body>
</html>`, eventTitle, changeSummary, eventDate, eventLocation, groupName, url)
}

// ---- Helpers ----

// formatTimeOnly extracts the time portion from a pre-formatted date string.
// For a string like "Mon, Jul 15, 2026 6:00 PM" it returns "6:00 PM".
func formatTimeOnly(dateStr string) string {
	parts := strings.Fields(dateStr)
	if len(parts) < 2 {
		return dateStr
	}
	// Return the last two space-separated tokens (e.g. "6:00 PM")
	return parts[len(parts)-2] + " " + parts[len(parts)-1]
}

// FormatEventDate converts a unix timestamp to a human-readable string
// suitable for email bodies.
func FormatEventDate(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("Mon, Jan 2, 2006 3:04 PM MST")
}