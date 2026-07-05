// Convert all <time> elements to the user's local timezone.
// Server renders times in the server timezone; this script rewrites
// them client-side using the datetime attribute.
(function() {
  function convertTimes() {
    document.querySelectorAll('time[datetime]').forEach(function(el) {
      var dt = el.getAttribute('datetime');
      if (!dt) return;
      var date = new Date(dt);
      if (isNaN(date.getTime())) return;

      var fmt = el.getAttribute('data-format') || 'datetime';
      var opts;
      switch (fmt) {
        case 'date':
          opts = { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' };
          break;
        case 'time':
          opts = { hour: 'numeric', minute: '2-digit' };
          break;
        case 'short':
          opts = { month: 'short', day: 'numeric' };
          break;
        case 'relative':
          el.textContent = relativeTime(date);
          return;
        default:
          opts = { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' };
      }
      try {
        el.textContent = date.toLocaleString(undefined, opts);
      } catch(e) {
        // fallback
        el.textContent = date.toLocaleString();
      }
    });
  }

  function relativeTime(date) {
    var now = new Date();
    var diff = date - now;
    var absDiff = Math.abs(diff);
    var sec = Math.floor(absDiff / 1000);
    var min = Math.floor(sec / 60);
    var hr = Math.floor(min / 60);
    var day = Math.floor(hr / 24);

    if (diff > 0) {
      if (sec < 60) return 'Soon';
      if (min < 60) return 'In ' + min + ' min';
      if (hr < 24) return 'In ' + hr + ' hour' + (hr > 1 ? 's' : '');
      if (day === 1) return 'Tomorrow';
      if (day < 7) return 'In ' + day + ' days';
      if (day < 14) return 'Next week';
      if (day < 30) return 'In ' + Math.floor(day / 7) + ' weeks';
      return 'In ' + Math.floor(day / 30) + ' months';
    } else {
      if (sec < 60) return 'Just now';
      if (min < 60) return min + ' min ago';
      if (hr < 24) return hr + ' hour' + (hr > 1 ? 's' : '') + ' ago';
      if (day === 1) return 'Yesterday';
      if (day < 7) return day + ' days ago';
      if (day < 14) return 'Last week';
      if (day < 30) return Math.floor(day / 7) + ' weeks ago';
      return Math.floor(day / 30) + ' months ago';
    }
  }

  // Run on page load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', convertTimes);
  } else {
    convertTimes();
  }

  // Re-run after HTMX swaps
  document.body.addEventListener('htmx:afterSwap', convertTimes);
})();