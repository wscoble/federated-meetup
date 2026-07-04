// Recurrence toggle for the dashboard event creation form.
// Toggles visibility of recurrence options based on the dropdown selection.
document.addEventListener('DOMContentLoaded', function () {
  var sel = document.getElementById('recurrence_type');
  if (!sel) return;
  sel.addEventListener('change', function () {
    var opts = document.getElementById('recurrence-options');
    if (!opts) return;
    if (sel.value && sel.value !== 'none') {
      opts.classList.remove('hidden');
    } else {
      opts.classList.add('hidden');
    }
  });
});