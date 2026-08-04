(function () {
  var STORAGE_KEY = 'ikite.prefs';
  var LAST_ALERT_KEY = 'ikite.prefs.lastAlertAt';

  function loadPrefs() {
    try {
      var raw = localStorage.getItem(STORAGE_KEY);
      return raw ? JSON.parse(raw) : null;
    } catch (e) {
      return null;
    }
  }

  function lastAlertAt() {
    var v = localStorage.getItem(LAST_ALERT_KEY);
    if (!v) return 0;
    var n = parseInt(v, 10);
    return isNaN(n) ? 0 : n;
  }

  function setLastAlertAt(ms) {
    localStorage.setItem(LAST_ALERT_KEY, String(ms));
  }

  function fmtSide(spot) {
    if (!spot || spot.gust <= 0) return '0';
    return String(Math.round(spot.gust));
  }

  function fmtCenter(spot) {
    if (!spot) return '0';
    return Math.round(spot.wind) + ' - ' + Math.round(spot.gust);
  }

  function inAlertHours(prefs) {
    var start = prefs.notifications.alertStartHour != null ? prefs.notifications.alertStartHour : 8;
    var end = prefs.notifications.alertEndHour != null ? prefs.notifications.alertEndHour : 17;
    var h = new Date().getHours();
    return h >= start && h <= end;
  }

  function poll() {
    var prefs = loadPrefs();
    if (!prefs || !prefs.notifications || !prefs.notifications.enabled) return;

    var n = prefs.notifications;
    var threshold = parseFloat(n.threshold);
    if (isNaN(threshold) || threshold >= 999) return;

    var spots = [n.spotLeft, n.spotCenter, n.spotRight].filter(function (s) { return s; }).join(',');
    if (!spots || !n.spotCenter) return;

    fetch('/api/wind/live?spots=' + encodeURIComponent(spots), { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var byKey = data.spots || {};
        var center = byKey[n.spotCenter];
        if (!center) return;

        var wind = center.wind || 0;
        if (wind < threshold) return;
        if (!inAlertHours(prefs)) return;

        var intervalMin = parseInt(n.intervalMin, 10);
        if (isNaN(intervalMin) || intervalMin < 1) intervalMin = 5;
        var intervalMs = intervalMin * 60 * 1000;
        var now = Date.now();
        if (now - lastAlertAt() < intervalMs) return;

        if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;

        var left = byKey[n.spotLeft];
        var right = byKey[n.spotRight];
        var body = fmtSide(left) + ' | ' + fmtCenter(center) + ' | ' + fmtSide(right);
        var title = 'ikite wind alert';
        try {
          new Notification(title, { body: body, tag: 'ikite-wind-alert', renotify: true });
          setLastAlertAt(now);
        } catch (e) {
          /* ignore */
        }
      })
      .catch(function () { /* ignore network errors */ });
  }

  function schedule() {
    var prefs = loadPrefs();
    if (!prefs || !prefs.notifications || !prefs.notifications.enabled) return;
    if (typeof Notification === 'undefined') return;

    var intervalMin = parseInt(prefs.notifications.intervalMin, 10);
    if (isNaN(intervalMin) || intervalMin < 1) intervalMin = 5;

    poll();
    setInterval(poll, intervalMin * 60 * 1000);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', schedule);
  } else {
    schedule();
  }

  window.ikiteNotifications = {
    loadPrefs: loadPrefs,
    poll: poll,
    requestPermission: function () {
      if (typeof Notification === 'undefined') return Promise.resolve('unsupported');
      return Notification.requestPermission();
    }
  };
})();
