(function (global) {
  var PREFS_VERSION = 3;
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

  // Insert missing server keys near their default neighbors (e.g. ky-ims after ky).
  function insertMissingKeys(order, serverOrder) {
    var present = {};
    order.forEach(function (k) { present[k] = true; });
    serverOrder.forEach(function (k, i) {
      if (present[k]) return;
      var inserted = false;
      for (var j = i - 1; j >= 0; j--) {
        var prev = serverOrder[j];
        var idx = order.indexOf(prev);
        if (idx >= 0) {
          order.splice(idx + 1, 0, k);
          inserted = true;
          break;
        }
      }
      if (!inserted) order.push(k);
      present[k] = true;
    });
    return order;
  }

  function mergeCollectLayout(spots, opts) {
    opts = opts || {};
    var server = serverLayout(spots);
    var prefs = loadPrefs();
    if (!prefs || typeof prefs !== 'object') {
      prefs = {};
    }

    var known = {};
    spots.forEach(function (s) { known[s.key] = true; });

    if (!prefs.collectOrder || !prefs.collectOrder.length) {
      prefs.collectOrder = server.order.slice();
    } else {
      prefs.collectOrder = prefs.collectOrder.filter(function (k) { return known[k]; });
      prefs.collectOrder = insertMissingKeys(prefs.collectOrder, server.order);
    }

    if (!prefs.collectVisible) {
      prefs.collectVisible = {};
    }
    spots.forEach(function (s) {
      if (prefs.collectVisible[s.key] === undefined) {
        prefs.collectVisible[s.key] = s.display !== false;
      }
    });

    if (!prefs.prefsVersion || prefs.prefsVersion < PREFS_VERSION) {
      prefs.prefsVersion = PREFS_VERSION;
      if (opts.saveOnMigrate) {
        savePrefs(prefs);
      }
    }

    return prefs;
  }

  function displayOrder(spots, prefs) {
    prefs = prefs || mergeCollectLayout(spots);
    return prefs.collectOrder.filter(function (k) {
      if (prefs.collectVisible[k] === false) return false;
      // An IMS column (ky-ims, hp-ims…) follows its spot: Prefs has no toggle
      // for it, so it must not show when the spot itself is hidden.
      if (/-ims$/.test(k) && prefs.collectVisible[k.slice(0, -4)] === false) return false;
      return true;
    });
  }

  function rowHasVisibleData(tr) {
    if (!tr || !tr.cells) return false;
    for (var i = 1; i < tr.cells.length; i++) {
      var cell = tr.cells[i];
      if (!cell) continue;
      // Wind cells render a .speed value; empty placeholders have no content.
      if (cell.querySelector('.speed')) return true;
      var text = (cell.textContent || '').replace(/\s+/g, '');
      if (text) return true;
    }
    return false;
  }

  function columnHasVisibleData(table, colIndex) {
    var rows = table.querySelectorAll('tbody tr');
    for (var r = 0; r < rows.length; r++) {
      var tr = rows[r];
      if (tr.hidden) continue;
      var cell = tr.cells[colIndex];
      if (!cell) continue;
      if (cell.querySelector('.speed')) return true;
      var text = (cell.textContent || '').replace(/\s+/g, '');
      if (text) return true;
    }
    return false;
  }

  function hideEmptyColumns(table) {
    var headerRow = table.querySelector('thead tr');
    if (!headerRow) return;
    // Walk right-to-left so cellIndex stays valid while removing.
    for (var i = headerRow.cells.length - 1; i >= 1; i--) {
      if (columnHasVisibleData(table, i)) continue;
      table.querySelectorAll('tr').forEach(function (tr) {
        if (tr.cells[i]) tr.removeChild(tr.cells[i]);
      });
    }
  }

  function applyTableLayout(keys) {
    var table = document.querySelector('#windTable') || document.querySelector('table');
    if (!table) return;
    var headerRow = table.querySelector('thead tr');
    if (!headerRow) return;
    var keyToIndex = {};
    headerRow.querySelectorAll('[data-spot]').forEach(function (cell) {
      keyToIndex[cell.getAttribute('data-spot')] = cell.cellIndex;
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
      // After column prefs hide spots, drop rows that no longer show any reading.
      if (tr.parentNode && tr.parentNode.tagName === 'TBODY') {
        tr.hidden = !rowHasVisibleData(tr);
      }
    });
    hideEmptyColumns(table);
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
