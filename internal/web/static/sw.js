// Service Worker for Federated Meetup Web Push Notifications
// SPDX-License-Identifier: AGPL-3.0

const CACHE_NAME = 'fedmeetup-push-v1';

// Install — activate immediately.
self.addEventListener('install', (event) => {
  self.skipWaiting();
});

// Activate — take control immediately.
self.addEventListener('activate', (event) => {
  event.waitUntil(clients.claim());
});

// Push event handler — display a notification when a push message is received.
self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (e) {
    // Fallback: treat as plain text.
    data = { title: 'Federated Meetup', body: event.data ? event.data.text() : '' };
  }

  const title = data.title || 'Federated Meetup';
  const options = {
    body: data.body || '',
    icon: '/static/icon-192.png',
    badge: '/static/badge-72.png',
    data: data.url || '/',
    vibrate: [200, 100, 200],
    tag: data.tag || 'fedmeetup-notification',
    renotify: true,
  };

  event.waitUntil(self.registration.showNotification(title, options));
});

// Notification click — focus or open the app.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const targetUrl = event.notification.data || '/';

  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientList) => {
      // Focus an existing window if one exists.
      for (const client of clientList) {
        if (client.url.includes(self.location.origin) && 'focus' in client) {
          client.navigate(targetUrl);
          return client.focus();
        }
      }
      // Otherwise open a new window.
      if (clients.openWindow) {
        return clients.openWindow(targetUrl);
      }
    })
  );
});