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

  function loadLastAlertMap() {
    var raw = localStorage.getItem(LAST_ALERT_KEY);
    if (!raw) return {};
    try {
      var parsed = JSON.parse(raw);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) return parsed;
      if (typeof parsed === 'number' && !isNaN(parsed)) return { _legacy: parsed };
    } catch (e) {
      var n = parseInt(raw, 10);
      if (!isNaN(n)) return { _legacy: n };
    }
    return {};
  }

  function saveLastAlertMap(map) {
    localStorage.setItem(LAST_ALERT_KEY, JSON.stringify(map));
  }

  function newRuleId() {
    return 'r' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
  }

  function fmtSide(spot) {
    if (!spot || spot.gust <= 0) return '0';
    return String(Math.round(spot.gust));
  }

  function fmtCenter(spot) {
    if (!spot) return '0';
    return Math.round(spot.wind) + ' - ' + Math.round(spot.gust);
  }

  function parseThreshold(v, fallback) {
    var n = parseFloat(v);
    if (isNaN(n)) return fallback;
    return n;
  }

  function parseIntervalMin(v, fallback) {
    var n = parseInt(v, 10);
    if (isNaN(n) || n < 1) return fallback;
    return n;
  }

  /**
   * Migrate legacy single-spot / global threshold fields into per-rule settings.
   * Each rule: { id, enabled, threshold, intervalMin, label, spotLeft, spotCenter, spotRight }
   */
  function normalizeRules(n) {
    if (!n || typeof n !== 'object') return [];
    var legacyEnabled = !!n.enabled;
    var legacyThreshold = parseThreshold(n.threshold, 10);
    var legacyInterval = parseIntervalMin(n.intervalMin, 5);

    var rules = Array.isArray(n.rules) ? n.rules.slice() : [];
    if (!rules.length && (n.spotCenter || n.spotLeft || n.spotRight)) {
      rules.push({
        label: n.label || '',
        spotLeft: n.spotLeft || '',
        spotCenter: n.spotCenter || '',
        spotRight: n.spotRight || ''
      });
    }
    if (!rules.length) {
      rules.push({ label: '', spotLeft: '', spotCenter: '', spotRight: '' });
    }

    return rules.map(function (r) {
      r = r || {};
      var hasOwnEnabled = Object.prototype.hasOwnProperty.call(r, 'enabled');
      var hasOwnThreshold = Object.prototype.hasOwnProperty.call(r, 'threshold');
      var hasOwnInterval = Object.prototype.hasOwnProperty.call(r, 'intervalMin');
      return {
        id: r.id || newRuleId(),
        enabled: hasOwnEnabled ? !!r.enabled : legacyEnabled,
        threshold: hasOwnThreshold ? parseThreshold(r.threshold, legacyThreshold) : legacyThreshold,
        intervalMin: hasOwnInterval ? parseIntervalMin(r.intervalMin, legacyInterval) : legacyInterval,
        label: r.label != null ? String(r.label) : '',
        spotLeft: r.spotLeft || '',
        spotCenter: r.spotCenter || '',
        spotRight: r.spotRight || ''
      };
    });
  }

  function anyRuleEnabled(rules) {
    for (var i = 0; i < rules.length; i++) {
      if (rules[i].enabled && rules[i].spotCenter) return true;
    }
    return false;
  }

  function collectSpotKeys(rules) {
    var keys = [];
    var seen = {};
    rules.forEach(function (r) {
      [r.spotLeft, r.spotCenter, r.spotRight].forEach(function (k) {
        if (!k || seen[k]) return;
        seen[k] = true;
        keys.push(k);
      });
    });
    return keys;
  }

  /** Build "left | center | right", omitting empty side slots. */
  function formatRuleWind(byKey, rule) {
    byKey = byKey || {};
    var parts = [];
    if (rule.spotLeft) parts.push(fmtSide(byKey[rule.spotLeft]));
    parts.push(fmtCenter(byKey[rule.spotCenter]));
    if (rule.spotRight) parts.push(fmtSide(byKey[rule.spotRight]));
    return parts.join(' | ');
  }

  function formatRuleLine(byKey, rule, nowPrefix) {
    var wind = formatRuleWind(byKey, rule);
    var label = (rule.label || '').trim();
    var core = nowPrefix ? (nowPrefix + ': ' + wind) : wind;
    return label ? (label + ': ' + core) : core;
  }

  function inAlertHours(prefs) {
    var n = prefs.notifications || {};
    var start = n.alertStartHour != null ? n.alertStartHour : 8;
    var end = n.alertEndHour != null ? n.alertEndHour : 17;
    var h = new Date().getHours();
    return h >= start && h <= end;
  }

  function ruleDue(rule, lastMap, now) {
    var intervalMin = parseIntervalMin(rule.intervalMin, 5);
    var last = lastMap[rule.id];
    if (last == null && lastMap._legacy != null) last = lastMap._legacy;
    if (last == null) return true;
    return now - last >= intervalMin * 60 * 1000;
  }

  function poll() {
    var prefs = loadPrefs();
    if (!prefs || !prefs.notifications) return;

    var rules = normalizeRules(prefs.notifications).filter(function (r) {
      return r.enabled && r.spotCenter && parseThreshold(r.threshold, 10) < 999;
    });
    if (!rules.length) return;

    var spots = collectSpotKeys(rules).join(',');
    if (!spots) return;

    fetch('/api/wind/live?spots=' + encodeURIComponent(spots), { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var byKey = data.spots || {};
        if (!inAlertHours(prefs)) return;
        if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;

        var now = Date.now();
        var lastMap = loadLastAlertMap();
        var matching = rules.filter(function (rule) {
          var center = byKey[rule.spotCenter];
          if (!center) return false;
          var threshold = parseThreshold(rule.threshold, 10);
          if ((center.wind || 0) < threshold) return false;
          return ruleDue(rule, lastMap, now);
        });
        if (!matching.length) return;

        var body = matching.map(function (rule) {
          var wind = formatRuleWind(byKey, rule);
          var label = (rule.label || '').trim();
          return label ? (label + ': ' + wind) : wind;
        }).join('\n');

        var title = (window.I18N && window.I18N['js.wind_alert_title']) || 'ikite wind alert';
        try {
          new Notification(title, { body: body, tag: 'ikite-wind-alert', renotify: true });
          matching.forEach(function (rule) {
            lastMap[rule.id] = now;
          });
          delete lastMap._legacy;
          saveLastAlertMap(lastMap);
        } catch (e) {
          /* ignore */
        }
      })
      .catch(function () { /* ignore network errors */ });
  }

  var scheduled = false;
  var pollTimer = null;

  function schedule() {
    if (scheduled) return;
    var prefs = loadPrefs();
    if (!prefs || !prefs.notifications) return;
    if (typeof Notification === 'undefined') return;

    var rules = normalizeRules(prefs.notifications);
    if (!anyRuleEnabled(rules)) return;

    var minInterval = 5;
    rules.forEach(function (r) {
      if (!r.enabled || !r.spotCenter) return;
      var iv = parseIntervalMin(r.intervalMin, 5);
      if (iv < minInterval) minInterval = iv;
    });

    // Do not fetch wind on initial page load — the table is SSR from cron data.
    // First notification poll waits one full (shortest) interval.
    scheduled = true;
    pollTimer = setInterval(poll, minInterval * 60 * 1000);
  }

  if (!window.ikiteNotifications) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', schedule);
    } else {
      schedule();
    }
  }

  window.ikiteNotifications = {
    loadPrefs: loadPrefs,
    poll: poll,
    schedule: schedule,
    normalizeRules: normalizeRules,
    collectSpotKeys: collectSpotKeys,
    formatRuleWind: formatRuleWind,
    formatRuleLine: formatRuleLine,
    anyRuleEnabled: anyRuleEnabled,
    requestPermission: function () {
      if (typeof Notification === 'undefined') return Promise.resolve('unsupported');
      return Notification.requestPermission();
    }
  };
})();
