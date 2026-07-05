// Keyboard shortcuts for Federated Meetup.
// / — focus search
// g — go home
// t — toggle theme
// d — dashboard
// r — my RSVPs
// Escape — blur active element
(function() {
  var KEY_MAP = {
    'g': '/',
    'd': '/dashboard',
    'r': '/my-rsvps'
  };

  document.addEventListener('keydown', function(e) {
    // Don't interfere with typing
    var tag = (e.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') {
      if (e.key === 'Escape') e.target.blur();
      return;
    }

    // Don't interfere with modifier combos
    if (e.ctrlKey || e.metaKey || e.altKey) return;

    switch (e.key) {
      case '/':
        e.preventDefault();
        var search = document.getElementById('search-input');
        if (search) {
          search.focus();
          search.select();
        } else {
          // Redirect to home with search
          window.location.href = '/#search';
        }
        break;
      case 'g':
      case 'd':
      case 'r':
        var path = KEY_MAP[e.key];
        if (path && !e.repeat) {
          window.location.href = path;
        }
        break;
      case 't':
        if (!e.repeat) {
          var btn = document.querySelector('.theme-toggle');
          if (btn) btn.click();
        }
        break;
    }
  });
})();