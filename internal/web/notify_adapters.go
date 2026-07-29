// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"context"
	"database/sql"

	"github.com/wscoble/federated-meetup/internal/email"
	"github.com/wscoble/federated-meetup/internal/notify"
	"github.com/wscoble/federated-meetup/internal/notify/push"
)

// DB returns the underlying *sql.DB from the store. Used by the
// notification system to create its tables in the same database.
func (s *Store) DB() *sql.DB {
	return s.db
}

// ResolveUserForNotify looks up a user's notification profile for the
// queue processor. Exposed publicly so main.go can pass it as a
// resolver callback.
func (s *Server) ResolveUserForNotify(ctx context.Context, userEmail string) (notify.UserProfile, error) {
	return s.resolveUserForNotify(ctx, userEmail)
}

// ---- Sender adapters ----

// emailSenderAdapter wraps an email.EmailSender to implement the
// notify.EmailSender interface.
type emailSenderAdapter struct {
	sender email.EmailSender
}

// WrapEmailSender wraps an email.EmailSender as a notify.EmailSender.
func WrapEmailSender(sender email.EmailSender) notify.EmailSender {
	if sender == nil {
		return nil
	}
	return &emailSenderAdapter{sender: sender}
}

func (a *emailSenderAdapter) Send(ctx context.Context, to, subject, body string) error {
	return a.sender.Send(ctx, to, subject, body)
}

// pushSenderAdapter wraps a *push.Manager to implement the
// notify.PushSender interface.
type pushSenderAdapter struct {
	mgr *push.Manager
}

// WrapPushSender wraps a *push.Manager as a notify.PushSender.
func WrapPushSender(mgr *push.Manager) notify.PushSender {
	if mgr == nil {
		return nil
	}
	return &pushSenderAdapter{mgr: mgr}
}

func (a *pushSenderAdapter) Send(ctx context.Context, subscriptionJSON, title, body string) error {
	return a.mgr.Send(ctx, subscriptionJSON, title, body)
}