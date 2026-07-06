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
	tplOrganizerNotify = template.Must(template.New("organizer_notify").Parse(organizerNotifyTpl))
	tplMyRsvpsLink     = template.Must(template.New("my_rsvps_link").Parse(myRsvpsLinkTpl))
)

// RenderRsvpConfirm renders the RSVP confirmation (magic-link) email body.
func RenderRsvpConfirm(data RsvpConfirmData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplRsvpConfirm.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render rsvp_confirm: %w", err)
	}
	return fmt.Sprintf("Confirm your RSVP for %s", data.EventTitle), buf.String(), nil
}

// RenderRsvpConfirmed renders the RSVP-confirmed email body.
func RenderRsvpConfirmed(data RsvpConfirmedData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplRsvpConfirmed.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render rsvp_confirmed: %w", err)
	}
	return fmt.Sprintf("Your RSVP is confirmed for %s", data.EventTitle), buf.String(), nil
}

// RenderEventReminder renders the 24h-before reminder email body.
func RenderEventReminder(data EventReminderData) (subject, body string, err error) {
	var buf bytes.Buffer
	if err := tplEventReminder.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("email: render event_reminder: %w", err)
	}
	return fmt.Sprintf("Reminder: %s tomorrow at %s", data.EventTitle, formatTimeOnly(data.EventDate)), buf.String(), nil
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