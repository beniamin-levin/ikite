// Live wind map: our meters' latest readings on an OpenFreeMap vector map,
// with wind particles drifting near the meters that reported in the last hour.
(function () {
  'use strict';
  var cfg = window.LIVE_MAP || { spots: [] };
  var I = window.I18N || {};
  var t = function (k) { return I[k] || k; };
  var fmt = function (k, v) { return t(k).replace(/%[sd]/, v); };

  var FRESH_MIN = 15;          // green dot
  var LIVE_MIN = 60;           // orange dot; older readings are dimmed and drive no particles
  var REACH_KM = 8;            // particles at full strength this close to a meter…
  var FADE_KM = 25;            // …and none beyond this
  var REFRESH_MS = 60 * 1000;

  var dark = cfg.theme !== 'light';
  var main = document.getElementById('liveMain');
  var header = document.getElementById('site-header');

  function fitHeight() {
    var top = header ? header.getBoundingClientRect().bottom : 0;
    main.style.height = Math.max(320, window.innerHeight - top) + 'px';
  }
  fitHeight();
  window.addEventListener('resize', fitHeight);

  if (!window.maplibregl) return;
  maplibregl.setRTLTextPlugin('https://unpkg.com/@mapbox/mapbox-gl-rtl-text@0.2.3/mapbox-gl-rtl-text.js', true);

  // Opens on the northern Israeli spots, Betzet to Sdot Yam and the Kinneret;
  // the rest of the world's spots are a zoom out away.
  var view = {
    bounds: [[34.8815, 32.4732], [35.575, 33.078]],
    fitBoundsOptions: { padding: { top: 140, bottom: 60, left: 60, right: 60 }, maxZoom: 10 }
  };
  var map = new maplibregl.Map(Object.assign({
    container: 'liveMap',
    style: 'https://tiles.openfreemap.org/styles/' + (dark ? 'dark' : 'positron'),
    minZoom: 0,
    maxZoom: 14,
    attributionControl: { compact: true },
    dragRotate: false,
    pitchWithRotate: false
  }, view));
  map.touchZoomRotate.disableRotation();
  map.addControl(new maplibregl.NavigationControl({ showCompass: false }), cfg.lang === 'he' ? 'top-left' : 'top-right');

  // Place names in the page language.
  map.on('style.load', function () {
    var name = cfg.lang === 'he'
      ? ['coalesce', ['get', 'name:he'], ['get', 'name']]
      : ['coalesce', ['get', 'name:en'], ['get', 'name_en'], ['get', 'name:latin'], ['get', 'name']];
    map.getStyle().layers.forEach(function (l) {
      if (l.type !== 'symbol' || !l.layout || !l.layout['text-field']) return;
      if (JSON.stringify(l.layout['text-field']).indexOf('name') < 0) return;
      map.setLayoutProperty(l.id, 'text-field', name);
    });
  });

  // —— Readings ——
  function ageMin(s) { return s.at ? (Date.now() - new Date(s.at).getTime()) / 60000 : Infinity; }
  function freshness(s) {
    var a = ageMin(s);
    return a <= FRESH_MIN ? 'fresh' : a <= LIVE_MIN ? 'late' : 'old';
  }
  function agoText(s) {
    var a = Math.max(0, Math.round(ageMin(s)));
    if (a < 2) return t('livemap.now');
    if (a < 90) return fmt('livemap.ago_min', a);
    return fmt('livemap.ago_h', Math.round(a / 60));
  }
  function clock(s) {
    var d = new Date(s.at);
    return d.toLocaleTimeString(cfg.lang === 'he' ? 'he-IL' : 'en-GB', { hour: '2-digit', minute: '2-digit', timeZone: 'Asia/Jerusalem' });
  }
  var COMPASS = ['N', 'NNE', 'NE', 'ENE', 'E', 'ESE', 'SE', 'SSE', 'S', 'SSW', 'SW', 'WSW', 'W', 'WNW', 'NW', 'NNW'];
  function compass(d) { return d < 0 ? '' : COMPASS[Math.round(d / 22.5) % 16]; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }

  // —— Pins ——
  var pins = [];
  var popup = new maplibregl.Popup({ offset: 12, maxWidth: '260px' });

  function popupHTML(s) {
    var dir = s.dir >= 0 ? compass(s.dir) + ' ' + s.dir + '°' : '';
    var reading = s.at
      ? '<span class="big">' + s.wind + '–' + s.gust + '</span> kt' +
        '<div class="meta">' + (dir ? dir + ' · ' : '') + clock(s) + ' (' + agoText(s) + ')</div>'
      : '<div class="meta">' + t('livemap.no_reading') + '</div>';
    return '<div class="lm-pop"><b>' + esc(s.name) + '</b>' + reading +
      '<a href="/">' + t('livemap.table') + '</a>' +
      '<a href="/forecast?spot=' + encodeURIComponent(s.id) + '">' + t('livemap.forecast') + '</a>' +
      (s.camera ? '<a href="' + esc(s.camera) + '">' + t('livemap.camera') + '</a>' : '') + '</div>';
  }
  function pinName(s) { return s.name + (s.at ? ' ' + s.wind + '–' + s.gust + ' kt' : ''); }

  function pinElement(s) {
    var el = document.createElement('div');
    var fr = freshness(s);
    el.className = 'lm-pin' + (fr === 'old' ? ' is-old' : '');
    el.dataset.side = 'top';
    el.setAttribute('role', 'button');
    el.setAttribute('tabindex', '0');
    var arrow = s.dir >= 0 ? '<i class="wind_arrow" style="--dir:' + (s.dir + 180) + 'deg" aria-hidden="true"></i>' : '';
    var reading = s.at ? '<i class="lm-dot ' + fr + '"></i>' + arrow +
      '<strong>' + s.wind + '</strong><small>–' + s.gust + '</small>' : '<small>–</small>';
    el.innerHTML = '<span class="lm-point"></span>' +
      '<span class="lm-label ' + esc(s.level || 'lm-none') + '">' + reading +
      '<span class="lm-name">' + esc(s.name) + '</span></span>';
    var open = function (e) {
      e.stopPropagation();
      popup.setLngLat([s.lon, s.lat]).setHTML(popupHTML(s)).addTo(map);
    };
    el.addEventListener('click', open);
    el.addEventListener('keydown', function (e) { if (e.key === 'Enter' || e.key === ' ') open(e); });
    return el;
  }

  function drawPins(spots) {
    pins.forEach(function (p) { p.marker.remove(); });
    pins = spots.map(function (s) {
      var el = pinElement(s);
      var marker = new maplibregl.Marker({ element: el, anchor: 'center' }).setLngLat([s.lon, s.lat]).addTo(map);
      el.setAttribute('aria-label', pinName(s)); // Marker sets "Map marker"
      return { spot: s, el: el, marker: marker };
    });
    placeLabels();
  }

  // Each label goes on the first side (top, right, left, bottom) that does not
  // cover a label already placed, fresher readings first; KY and KH sit 2.5 km
  // apart and would stack otherwise. A label with no clear side is hidden,
  // leaving its dot (zoomed out, the Israeli spots all share a few pixels).
  function placeLabels() {
    var order = pins.slice().sort(function (a, b) { return ageMin(a.spot) - ageMin(b.spot); });
    var placed = [];
    var overlaps = function (r) {
      return placed.some(function (q) { return r.left < q.right && r.right > q.left && r.top < q.bottom && r.bottom > q.top; });
    };
    order.forEach(function (p) {
      var sides = ['top', 'right', 'left', 'bottom'];
      var label = p.el.querySelector('.lm-label');
      var chosen = null;
      for (var i = 0; i < sides.length && !chosen; i++) {
        p.el.dataset.side = sides[i];
        var r = label.getBoundingClientRect();
        if (!overlaps(r)) chosen = r;
      }
      if (!chosen) { p.el.dataset.side = 'none'; return; }
      placed.push(chosen);
    });
  }

  var updated = document.getElementById('lmUpdated');
  function showUpdated(spots) {
    var read = spots.filter(function (s) { return s.at; });
    if (!read.length) { updated.textContent = t('livemap.no_data'); return; }
    var newest = read.reduce(function (a, b) { return new Date(a.at) > new Date(b.at) ? a : b; });
    updated.textContent = fmt('livemap.updated', clock(newest) + ' (' + agoText(newest) + ')');
  }

  var spots = cfg.spots || [];
  function apply(next) {
    spots = next;
    drawPins(spots);
    showUpdated(spots);
    buildField();
  }

  function refresh() {
    fetch('/api/map/live', { cache: 'no-store' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d && d.spots) apply(d.spots); })
      .catch(function () {});
  }

  map.on('load', function () {
    apply(spots);
    setInterval(function () { if (!document.hidden) refresh(); }, REFRESH_MS);
    document.addEventListener('visibilitychange', function () { if (!document.hidden) refresh(); });
  });
  map.on('zoomend', placeLabels);

  // —— Particles ——
  // The wind between meters is an inverse-distance blend of the meters that
  // reported in the last hour, sampled on a coarse screen grid; it fades out
  // away from them so the map never draws wind nobody measured.
  var canvas = document.createElement('canvas');
  canvas.id = 'liveParticles';
  var container = map.getCanvasContainer();
  container.insertBefore(canvas, container.firstChild ? container.firstChild.nextSibling : null);
  var ctx = canvas.getContext('2d');
  var dpr = Math.min(window.devicePixelRatio || 1, 2);
  var CELL = 16;
  var field = null;
  var particles = [];
  var moving = false;
  var running = false;

  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var enabled = !reduced;
  try { var saved = localStorage.getItem('ikite.livemap.particles'); if (saved) enabled = saved === '1'; } catch (e) {}
  var toggle = document.getElementById('lmParticles');
  toggle.setAttribute('aria-pressed', enabled ? 'true' : 'false');
  toggle.addEventListener('click', function () {
    enabled = !enabled;
    toggle.setAttribute('aria-pressed', enabled ? 'true' : 'false');
    try { localStorage.setItem('ikite.livemap.particles', enabled ? '1' : '0'); } catch (e) {}
    if (enabled) start(); else clear();
  });

  function km(lat1, lon1, lat2, lon2) {
    var r = Math.PI / 180;
    var x = (lon2 - lon1) * r * Math.cos((lat1 + lat2) * r / 2);
    var y = (lat2 - lat1) * r;
    return Math.sqrt(x * x + y * y) * 6371;
  }

  // The canvas container is 0 px tall (MapLibre sizes only the GL canvas), so
  // the particle canvas takes the map's own box.
  var box = map.getContainer();
  function resizeCanvas() {
    var w = box.clientWidth, h = box.clientHeight;
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    canvas.style.width = w + 'px';
    canvas.style.height = h + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }

  function buildField() {
    resizeCanvas();
    var live = spots.filter(function (s) { return s.at && s.dir >= 0 && ageMin(s) <= LIVE_MIN; });
    if (!live.length) { field = null; clear(); return; }
    var w = box.clientWidth, h = box.clientHeight;
    var cols = Math.ceil(w / CELL) + 1, rows = Math.ceil(h / CELL) + 1;
    var u = new Float32Array(cols * rows), v = new Float32Array(cols * rows), f = new Float32Array(cols * rows);
    // Screen pixels per frame for one knot, the same at every zoom.
    var k = 0.12;
    for (var j = 0; j < rows; j++) {
      for (var i = 0; i < cols; i++) {
        var ll = map.unproject([i * CELL, j * CELL]);
        var su = 0, sv = 0, sw = 0, nearest = Infinity;
        for (var n = 0; n < live.length; n++) {
          var s = live[n];
          var d = Math.max(0.5, km(ll.lat, ll.lng, s.lat, s.lon));
          if (d < nearest) nearest = d;
          var wgt = 1 / (d * d);
          var to = (s.dir + 180) * Math.PI / 180; // the wind blows towards dir + 180
          su += Math.sin(to) * s.wind * wgt;
          sv += -Math.cos(to) * s.wind * wgt;
          sw += wgt;
        }
        var idx = j * cols + i;
        u[idx] = su / sw * k;
        v[idx] = sv / sw * k;
        f[idx] = nearest <= REACH_KM ? 1 : nearest >= FADE_KM ? 0 : 1 - (nearest - REACH_KM) / (FADE_KM - REACH_KM);
      }
    }
    field = { cols: cols, rows: rows, u: u, v: v, f: f, w: w, h: h };
    particles = [];
    var count = Math.min(2200, Math.round(w * h / 1000));
    for (var p = 0; p < count; p++) particles.push(spawn({}));
    start();
  }

  function sample(x, y) {
    var gx = x / CELL, gy = y / CELL;
    var i = Math.floor(gx), j = Math.floor(gy);
    if (i < 0 || j < 0 || i >= field.cols - 1 || j >= field.rows - 1) return null;
    var fx = gx - i, fy = gy - j, c = field.cols;
    var a = j * c + i, b = a + 1, d = a + c, e = d + 1;
    var lerp = function (arr) {
      return (arr[a] * (1 - fx) + arr[b] * fx) * (1 - fy) + (arr[d] * (1 - fx) + arr[e] * fx) * fy;
    };
    return { u: lerp(field.u), v: lerp(field.v), f: lerp(field.f) };
  }

  function spawn(p) {
    for (var tries = 0; tries < 12; tries++) {
      p.x = Math.random() * field.w;
      p.y = Math.random() * field.h;
      var s = sample(p.x, p.y);
      if (s && s.f > 0.05 && Math.random() < s.f) break;
    }
    p.age = 0;
    p.life = 40 + Math.floor(Math.random() * 70);
    return p;
  }

  function clear() { ctx.clearRect(0, 0, canvas.width, canvas.height); }

  function frame() {
    if (!running) return;
    if (!enabled || moving || !field) { running = false; return; }
    ctx.globalCompositeOperation = 'destination-out';
    ctx.fillStyle = 'rgba(0,0,0,0.07)';
    ctx.fillRect(0, 0, field.w, field.h);
    ctx.globalCompositeOperation = 'source-over';
    ctx.lineWidth = 1.3;
    ctx.lineCap = 'round';
    var rgb = dark ? '235,245,255' : '20,55,95';
    for (var n = 0; n < particles.length; n++) {
      var p = particles[n];
      var s = sample(p.x, p.y);
      if (!s || s.f <= 0.02 || p.age++ > p.life) { spawn(p); continue; }
      var nx = p.x + s.u, ny = p.y + s.v;
      ctx.strokeStyle = 'rgba(' + rgb + ',' + ((dark ? 0.75 : 0.5) * s.f).toFixed(2) + ')';
      ctx.beginPath();
      ctx.moveTo(p.x, p.y);
      ctx.lineTo(nx, ny);
      ctx.stroke();
      p.x = nx; p.y = ny;
    }
    requestAnimationFrame(frame);
  }

  function start() {
    if (running || !enabled || moving || !field) return;
    running = true;
    requestAnimationFrame(frame);
  }

  map.on('movestart', function () { moving = true; clear(); });
  map.on('moveend', function () { moving = false; buildField(); });
  map.on('resize', function () { if (!moving) buildField(); });
})();
