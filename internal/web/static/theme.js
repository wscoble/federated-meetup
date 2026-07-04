// Federated Meetup — theme toggle + a11y helpers
(function() {
  'use strict';

  // Theme toggle with localStorage persistence
  const STORAGE_KEY = 'fm-theme';
  
  function getTheme() {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'light' || stored === 'dark') return stored;
    return null; // follow system
  }

  function applyTheme(theme) {
    if (theme) {
      document.documentElement.setAttribute('data-theme', theme);
    } else {
      document.documentElement.removeAttribute('data-theme');
    }
  }

  // Apply stored theme on load
  applyTheme(getTheme());

  // Wire up toggle buttons
  document.addEventListener('DOMContentLoaded', function() {
    const toggles = document.querySelectorAll('.theme-toggle');
    toggles.forEach(function(btn) {
      btn.addEventListener('click', function() {
        const current = getTheme();
        const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
        const isDark = current === 'dark' || (current === null && prefersDark);
        const next = isDark ? 'light' : 'dark';
        localStorage.setItem(STORAGE_KEY, next);
        applyTheme(next);
      });
    });
  });
})();