// Toast notification system for Federated Meetup.
// Shows a temporary notification at the bottom of the screen.
// Listens for HTMX events and shows toasts on success/error.
(function() {
  // Create toast container if it doesn't exist
  function ensureContainer() {
    var c = document.getElementById('toast-container');
    if (!c) {
      c = document.createElement('div');
      c.id = 'toast-container';
      c.setAttribute('role', 'status');
      c.setAttribute('aria-live', 'polite');
      c.setAttribute('aria-atomic', 'true');
      document.body.appendChild(c);
    }
    return c;
  }

  function showToast(message, type) {
    var container = ensureContainer();
    var toast = document.createElement('div');
    toast.className = 'toast toast-' + (type || 'info');
    toast.setAttribute('role', 'alert');
    toast.textContent = message;
    container.appendChild(toast);

    // Animate in
    requestAnimationFrame(function() {
      toast.classList.add('toast-visible');
    });

    // Auto-remove after 4 seconds
    setTimeout(function() {
      toast.classList.remove('toast-visible');
      setTimeout(function() {
        if (toast.parentNode) toast.parentNode.removeChild(toast);
      }, 300);
    }, 4000);
  }

  // Expose globally
  window.fedmeetupToast = showToast;

  // Listen for HTMX events
  document.body.addEventListener('htmx:afterRequest', function(e) {
    // Only show toasts for POST requests (form submissions)
    if (e.detail.requestConfig.verb !== 'post') return;

    var target = e.detail.target;
    if (!target) return;

    // Check if the response contains a success alert
    var response = target.innerHTML || '';
    if (response.indexOf('alert-success') !== -1) {
      // Extract the message from the alert
      var match = response.match(/<p class="font-medium">([^<]+)<\/p>/);
      if (match) {
        showToast(match[1], 'success');
      }
    } else if (response.indexOf('alert-error') !== -1) {
      var match = response.match(/<p class="font-medium">([^<]+)<\/p>/);
      if (match) {
        showToast(match[1], 'error');
      }
    }
  });

  // Show toast on successful copy
  document.addEventListener('click', function(e) {
    var btn = e.target.closest('[aria-label*="Share"]');
    if (btn && navigator.clipboard) {
      // The share button copies to clipboard; show a toast
      setTimeout(function() {
        showToast('Link copied to clipboard!', 'success');
      }, 100);
    }
  });
})();