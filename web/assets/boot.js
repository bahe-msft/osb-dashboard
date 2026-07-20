(function () {
  var sidebar = localStorage.getItem('opensandbox-sidebar');
  if (sidebar) { document.documentElement.setAttribute('data-sidebar', sidebar); }
  var savedTheme = localStorage.getItem('opensandbox-theme');
  var preferredTheme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dim' : 'corporate';
  document.documentElement.setAttribute('data-theme', savedTheme || preferredTheme);
  window.osbLiveUpdatesEnabled = localStorage.getItem('opensandbox-live-updates') !== 'paused';
  window.osbSandboxFilter = localStorage.getItem('opensandbox-state-filter') || 'all';
})();
