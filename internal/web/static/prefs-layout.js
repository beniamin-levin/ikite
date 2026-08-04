(function (global) {
  var PREFS_VERSION = 2;
  var STORAGE_KEY = 'ikite.prefs';

  function loadPrefs() {
    try {
      var raw = localStorage.getItem(STORAGE_KEY);
      return raw ? JSON.parse(raw) : null;
    } catch (e) {
      return null;
    }
  }

  function savePrefs(prefs) {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  }

  function serverLayout(spots) {
    var order = spots.map(function (s) { return s.key; });
    var visible = {};
    spots.forEach(function (s) {
      visible[s.key] = s.display !== false;
    });
    return { order: order, visible: visible };
  }

  function mergeCollectLayout(spots, opts) {
    opts = opts || {};
    var server = serverLayout(spots);
    var prefs = loadPrefs();
    if (!prefs || typeof prefs !== 'object') {
      prefs = {};
    }

    if (!prefs.prefsVersion || prefs.prefsVersion < PREFS_VERSION) {
      prefs.collectOrder = server.order.slice();
      prefs.collectVisible = {};
      spots.forEach(function (s) {
        prefs.collectVisible[s.key] = s.display !== false;
      });
      prefs.prefsVersion = PREFS_VERSION;
      if (opts.saveOnMigrate) {
        savePrefs(prefs);
      }
    }

    if (!prefs.collectOrder || !prefs.collectOrder.length) {
      prefs.collectOrder = server.order.slice();
    } else {
      var known = {};
      spots.forEach(function (s) { known[s.key] = true; });
      prefs.collectOrder = prefs.collectOrder.filter(function (k) { return known[k]; });
      server.order.forEach(function (k) {
        if (prefs.collectOrder.indexOf(k) < 0) {
          prefs.collectOrder.push(k);
        }
      });
    }

    if (!prefs.collectVisible) {
      prefs.collectVisible = {};
    }
    spots.forEach(function (s) {
      if (prefs.collectVisible[s.key] === undefined) {
        prefs.collectVisible[s.key] = s.display !== false;
      }
    });

    return prefs;
  }

  function displayOrder(spots, prefs) {
    prefs = prefs || mergeCollectLayout(spots);
    return prefs.collectOrder.filter(function (k) {
      return prefs.collectVisible[k] !== false;
    });
  }

  function applyTableLayout(keys) {
    var table = document.querySelector('table');
    if (!table) return;
    var headerRow = table.querySelector('thead tr');
    if (!headerRow) return;
    var keyToIndex = {};
    headerRow.querySelectorAll('td[data-spot]').forEach(function (td) {
      keyToIndex[td.getAttribute('data-spot')] = td.cellIndex;
    });
    table.querySelectorAll('tr').forEach(function (tr) {
      if (!tr.cells.length) return;
      var timeCell = tr.cells[0];
      var spotCells = {};
      for (var key in keyToIndex) {
        spotCells[key] = tr.cells[keyToIndex[key]];
      }
      while (tr.firstChild) tr.removeChild(tr.firstChild);
      tr.appendChild(timeCell);
      keys.forEach(function (key) {
        if (spotCells[key]) tr.appendChild(spotCells[key]);
      });
    });
  }

  global.IkitePrefs = {
    PREFS_VERSION: PREFS_VERSION,
    STORAGE_KEY: STORAGE_KEY,
    loadPrefs: loadPrefs,
    savePrefs: savePrefs,
    serverLayout: serverLayout,
    mergeCollectLayout: mergeCollectLayout,
    displayOrder: displayOrder,
    applyTableLayout: applyTableLayout
  };
})(typeof window !== 'undefined' ? window : globalThis);
