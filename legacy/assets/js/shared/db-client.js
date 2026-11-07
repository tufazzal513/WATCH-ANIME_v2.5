/**
 * MoveX db client  -  drop-in replacement for the parts of firebase.database() the app uses.
 * Data now lives in the MoveX server (SQLite database.db). Firebase is used for sign-in only.
 *
 *   db.ref(path).on/once('value') · set · update · push · remove · transaction
 *   .orderByChild(k).limitToLast(n) · .child() · .off() · .onDisconnect() (no-op)
 *   firebase.database.ServerValue.TIMESTAMP
 *
 * How it saves load (this is the whole point):
 *  - no websocket / no always-on connection: plain HTTPS requests
 *  - every read sends If-None-Match -> the server answers "304 Not Modified" with an empty body
 *  - on('value') listeners refresh only every POLL_MS and only while the tab is visible,
 *    plus immediately after YOUR OWN writes to a related path
 *  - listeners on the same path share one request; unchanged data never re-renders the UI
 */
(function () {
  'use strict';
  var POLL_MS = 60000;
  var lastToken = null;

  function base() { return (typeof MX_API_BASE === 'string' ? MX_API_BASE : '').replace(/\/+$/, ''); }

  // ---- auth token (waits for Firebase to restore the session first) ----
  var authReadyP = null;
  function authReady() {
    if (!authReadyP) authReadyP = new Promise(function (res) {
      try { var un = firebase.auth().onAuthStateChanged(function () { try { un(); } catch (e) {} res(); }); }
      catch (e) { res(); }
    });
    return authReadyP;
  }
  function getToken(force) {
    return authReady().then(function () {
      var u = firebase.auth().currentUser;
      if (!u) { lastToken = null; return null; }
      return u.getIdToken(!!force).then(function (t) { lastToken = t; return t; }, function () { return null; });
    });
  }

  // ---- HTTP ----
  function encPath(p) { return p.split('/').filter(Boolean).map(encodeURIComponent).join('/'); }
  function mkErr(status, code) {
    var e = new Error(code || ('http_' + status));
    e.status = status;
    e.code = status === 403 ? 'PERMISSION_DENIED' : (status === 401 ? 'UNAUTHENTICATED' : String(code || status));
    return e;
  }
  // ---- which server to read from: a worker (fast, spreads load) or main; writes always go to main ----
  var readBase = null, badUntil = 0, stickMainUntil = 0, serversP = null;
  function curReadBase() {
    if (Date.now() < stickMainUntil || Date.now() < badUntil || readBase === null) return base();   // just wrote / worker failed -> main
    return readBase;
  }
  function pickWorker(list) {
    if (!list.length) return Promise.resolve(null);
    if (list.length === 1) return Promise.resolve(list[0].url.replace(/\/+$/, ''));
    return Promise.all(list.map(function (w) {      // several workers: use the quickest to answer
      var t0 = Date.now(), u = w.url.replace(/\/+$/, '');
      return fetch(u + '/api/health').then(function (r) { return r.ok ? { u: u, ms: Date.now() - t0 } : null; }, function () { return null; });
    })).then(function (rs) { rs = rs.filter(Boolean).sort(function (a, b) { return a.ms - b.ms; }); return rs.length ? rs[0].u : null; });
  }
  function loadServers() {
    if (serversP) return serversP;
    var cached = null;
    try { cached = JSON.parse(localStorage.getItem('mx_servers') || 'null'); } catch (e) {}
    var listP = (cached && Date.now() - cached.at < 300000) ? Promise.resolve(cached.list)
      : fetch(base() + '/api/servers').then(function (r) { return r.ok ? r.json() : { workers: [] }; }).then(function (j) {
          var l = (j && j.workers) || []; try { localStorage.setItem('mx_servers', JSON.stringify({ at: Date.now(), list: l })); } catch (e) {} return l;
        }).catch(function () { return []; });
    serversP = listP.then(pickWorker).then(function (u) { readBase = u; }, function () {});
    return serversP;
  }

  function request(method, path, o, retried) {
    o = o || {};
    var isRead = method === 'GET';
    var tokenP = o.sync ? Promise.resolve(lastToken) : getToken(!!retried);
    return Promise.all([tokenP, isRead ? loadServers() : null]).then(function (a) {
      var token = a[0];
      var h = {};
      if (token) h.Authorization = 'Bearer ' + token;
      if (o.body !== undefined) h['Content-Type'] = 'application/json';
      if (o.etag) h['If-None-Match'] = o.etag;
      var tail = '/api/db/' + encPath(path) + (o.query ? '?' + o.query : '');
      function go(root) {
        return fetch(root + tail, { method: method, headers: h, body: o.body !== undefined ? JSON.stringify(o.body) : undefined, keepalive: !!o.keepalive });
      }
      var root = isRead ? curReadBase() : base();
      var p = go(root);
      if (isRead && root !== base()) {      // worker down or warming up -> quietly fall back to main
        p = p.then(function (r) { if (r.status >= 502 && r.status <= 504) throw new Error('worker_down'); return r; })
             .catch(function () { badUntil = Date.now() + 120000; return go(base()); });
      }
      return p.then(function (r) {
          if (r.status === 304) return { notModified: true };
          if (r.status === 401 && !retried && !o.sync && firebase.auth().currentUser) return request(method, path, o, true);
          return r.text().then(function (txt) {
            var json = null; if (txt) { try { json = JSON.parse(txt); } catch (e) {} }
            if (!r.ok) throw mkErr(r.status, json && json.error);
            if (!isRead) stickMainUntil = Date.now() + 8000;   // read-your-own-writes: main is always current
            return { json: json, etag: r.headers.get('ETag') };
          });
        });
    });
  }

  // ---- persistent cache (IndexedDB): a page refresh shows the last data instantly, then only a tiny 304 check ----
  var idbP = null, uidKey = 'anon';
  var NO_PERSIST = /^(online_users|pwd_resets|analytics)(\/|$)/;
  function idb() {
    if (!idbP) idbP = new Promise(function (res) {
      try { var rq = indexedDB.open('mx-cache', 1); rq.onupgradeneeded = function () { rq.result.createObjectStore('kv'); }; rq.onsuccess = function () { res(rq.result); }; rq.onerror = function () { res(null); }; } catch (e) { res(null); }
    });
    return idbP;
  }
  function idbGet(k) {
    return idb().then(function (d) {
      if (!d) return null;
      return new Promise(function (res) { try { var r = d.transaction('kv').objectStore('kv').get(k); r.onsuccess = function () { res(r.result || null); }; r.onerror = function () { res(null); }; } catch (e) { res(null); } });
    });
  }
  function idbSet(k, v) { idb().then(function (d) { if (d) { try { d.transaction('kv', 'readwrite').objectStore('kv').put(v, k); } catch (e) {} } }); }
  function idbClear() { idb().then(function (d) { if (d) { try { d.transaction('kv', 'readwrite').objectStore('kv').clear(); } catch (e) {} } }); }

  // ---- shared GET cache: one request per path, conditional via ETag ----
  var cache = {}, inflight = {};
  function fetchText(path, query, onStale) {
    var key = path + '?' + (query || '');
    if (inflight[key]) return inflight[key];
    var persist = !NO_PERSIST.test(path);
    var p = authReady().then(function () {
      var u = null; try { u = firebase.auth().currentUser; } catch (e) {}
      var uk = u ? u.uid : 'anon';
      if (uk !== uidKey) { uidKey = uk; cache = {}; }       // different person -> never reuse their data
      var mem = cache[key];
      var stage = (mem || !persist) ? Promise.resolve(mem) : idbGet(uidKey + '|' + key).then(function (s) { if (s) cache[key] = s; return s; });
      return stage.then(function (e) {
        if (e && !mem && onStale) { try { onStale(e.text); } catch (er) { console.error(er); } }   // show cached data right away
        return request('GET', path, { query: query, etag: e && e.etag }).then(function (r) {
          if (r.notModified && e) return e.text;
          var text = JSON.stringify(r.json === undefined ? null : r.json);
          cache[key] = { etag: r.etag, text: text };
          if (persist && r.etag) idbSet(uidKey + '|' + key, cache[key]);
          return text;
        });
      });
    }).then(function (t) { delete inflight[key]; return t; }, function (err) { delete inflight[key]; throw err; });
    inflight[key] = p;
    return p;
  }

  // ---- snapshot ----
  function Snap(path, val) { this._p = path; this._v = val === undefined ? null : val; this.key = path.split('/').filter(Boolean).pop() || null; }
  Snap.prototype.val = function () { return this._v; };
  Snap.prototype.exists = function () { return this._v !== null && this._v !== undefined; };
  Snap.prototype.hasChildren = function () { return !!this._v && typeof this._v === 'object' && Object.keys(this._v).length > 0; };
  Snap.prototype.numChildren = function () { return this._v && typeof this._v === 'object' ? Object.keys(this._v).length : 0; };
  Snap.prototype.child = function (p) {
    var v = this._v;
    p.split('/').filter(Boolean).forEach(function (s) { v = v && typeof v === 'object' ? v[s] : undefined; });
    return new Snap(this._p + '/' + p, v);
  };
  Snap.prototype.forEach = function (cb) {
    if (!this._v || typeof this._v !== 'object') return false;
    var self = this, keys = Object.keys(this._v);
    for (var i = 0; i < keys.length; i++) if (cb(new Snap(self._p + '/' + keys[i], self._v[keys[i]])) === true) return true;
    return false;
  };

  // ---- listeners ----
  var listeners = [];
  var PUSH = '-0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz';
  function pushId() {
    var now = Date.now(), s = '';
    for (var i = 0; i < 8; i++) { s = PUSH.charAt(now % 64) + s; now = Math.floor(now / 64); }
    var r = new Uint8Array(12);
    (window.crypto || window.msCrypto).getRandomValues(r);
    for (var j = 0; j < 12; j++) s += PUSH.charAt(r[j] % 64);
    return s;
  }
  function norm(p) { return p.split('/').filter(Boolean).join('/'); }
  function related(a, b) { return a === '' || b === '' || a === b || a.indexOf(b + '/') === 0 || b.indexOf(a + '/') === 0; }

  function deliver(L, text) {
    L.errored = false;
    if (L.last === text) return;           // nothing changed -> no callback, no re-render
    L.last = text;
    try { L.cb(new Snap(L.path, JSON.parse(text))); } catch (e) { console.error(e); }
  }
  function refreshGroup(items) {
    var L0 = items[0];
    var stale = function (text) { items.forEach(function (L) { if (listeners.indexOf(L) >= 0 && L.last === undefined) deliver(L, text); }); };
    return fetchText(L0.path, L0.query, stale).then(function (text) {
      items.forEach(function (L) { if (listeners.indexOf(L) >= 0) deliver(L, text); });
    }, function (err) {
      items.forEach(function (L) {
        if (!L.errored) { L.errored = true; if (L.errCb) { try { L.errCb(err); } catch (e) {} } }
      });
    });
  }
  function refresh(filterPath) {
    var groups = {};
    listeners.forEach(function (L) {
      if (filterPath !== undefined && !related(L.path, filterPath)) return;
      var k = L.path + '?' + (L.query || '');
      (groups[k] = groups[k] || []).push(L);
    });
    return Promise.all(Object.keys(groups).map(function (k) { return refreshGroup(groups[k]); }));
  }
  var pendingRefresh = null, pendingPaths = [];
  function afterWrite(path) {
    pendingPaths.push(norm(path));
    if (pendingRefresh) return;
    pendingRefresh = setTimeout(function () {
      var paths = pendingPaths; pendingPaths = []; pendingRefresh = null;
      var groups = {};
      listeners.forEach(function (L) {
        if (!paths.some(function (p) { return related(L.path, p); })) return;
        var k = L.path + '?' + (L.query || '');
        (groups[k] = groups[k] || []).push(L);
      });
      Object.keys(groups).forEach(function (k) { refreshGroup(groups[k]); });
    }, 30);
  }

  var lastPoll = Date.now();
  setInterval(function () {
    if (document.visibilityState === 'hidden' || !listeners.length) return;
    lastPoll = Date.now(); refresh();
  }, POLL_MS);
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible' && listeners.length && Date.now() - lastPoll > 20000) { lastPoll = Date.now(); refresh(); }
  });
  try { firebase.auth().onAuthStateChanged(function (u) {
    if (!u && uidKey !== 'anon') { idbClear(); cache = {}; uidKey = 'anon'; }   // logged out: forget private data on this device
    if (listeners.length) refresh();
  }); } catch (e) {}

  // ---- Reference ----
  function Ref(path, q) { this.path = norm(path || ''); this._q = q || {}; this.key = this.path.split('/').pop() || null; }
  Ref.prototype._query = function () {
    var p = [];
    if (this._q.orderBy) p.push('orderBy=' + encodeURIComponent(this._q.orderBy));
    if (this._q.limit) p.push('limitToLast=' + this._q.limit);
    if (this._q.lite) p.push('lite=1');
    return p.join('&') || undefined;
  };
  Ref.prototype.child = function (p) { return new Ref(this.path + '/' + p); };
  Object.defineProperty(Ref.prototype, 'parent', { get: function () { var s = this.path.split('/'); s.pop(); return s.length ? new Ref(s.join('/')) : null; } });
  Object.defineProperty(Ref.prototype, 'root', { get: function () { return new Ref(''); } });
  Ref.prototype.lite = function () { return new Ref(this.path, { orderBy: this._q.orderBy, limit: this._q.limit, lite: true }); };   // movie list without episodes/links
  Ref.prototype.orderByChild = function (k) { return new Ref(this.path, { orderBy: k, limit: this._q.limit }); };
  Ref.prototype.limitToLast = function (n) { return new Ref(this.path, { orderBy: this._q.orderBy, limit: n }); };

  Ref.prototype.on = function (evt, cb, errCb) {
    if (evt !== 'value') throw new Error('MoveX db: only the "value" event is supported (got "' + evt + '")');
    if (this.path === '.info/connected') { var self = this; Promise.resolve().then(function () { cb(new Snap(self.path, true)); }); return cb; }
    var L = { path: this.path, query: this._query(), cb: cb, errCb: errCb, last: undefined, errored: false };
    listeners.push(L);
    refreshGroup([L]);
    return cb;
  };
  Ref.prototype.off = function (evt, cb) {
    var path = this.path, q = this._query();
    listeners = listeners.filter(function (L) { return !(L.path === path && L.query === q && (!cb || L.cb === cb)); });
  };
  Ref.prototype.once = function (evt, cb, errCb) {
    var self = this;
    var p = fetchText(this.path, this._query()).then(function (t) {
      var snap = new Snap(self.path, JSON.parse(t));
      if (cb) { try { cb(snap); } catch (e) { console.error(e); } }
      return snap;
    });
    if (cb || errCb) return p.catch(function (e) { if (errCb) errCb(e); });
    return p;
  };
  Ref.prototype.set = function (v) {
    var self = this;
    return request('PUT', this.path, { body: v === undefined ? null : v }).then(function () { afterWrite(self.path); });
  };
  Ref.prototype.update = function (obj) {
    var self = this;
    return request('PATCH', this.path, { body: obj }).then(function () { afterWrite(self.path); });
  };
  Ref.prototype.remove = function (opts) {
    var self = this;
    return request('DELETE', this.path, { keepalive: !!(opts && opts.keepalive), sync: !!(opts && opts.keepalive) })
      .then(function () { afterWrite(self.path); });
  };
  Ref.prototype.push = function (v) {
    var child = this.child(pushId());
    if (v === undefined) return child;
    var p = child.set(v);
    child.then = function (a, b) { return p.then(a, b); };
    child.catch = function (b) { return p.catch(b); };
    return child;
  };
  // read-modify-write; the server only accepts what the old Firebase rules accepted
  Ref.prototype.transaction = function (fn) {
    var self = this;
    return this.once('value').then(function (snap) {
      var next = fn(snap.val());
      if (next === undefined) return { committed: false, snapshot: snap };
      return self.set(next).then(function () { return { committed: true, snapshot: new Snap(self.path, next) }; });
    });
  };
  Ref.prototype.onDisconnect = function () {
    var ok = function () { return Promise.resolve(); };
    return { remove: ok, set: ok, update: ok, cancel: ok };   // there is no persistent connection to detect
  };

  window.mxCreateDb = function () {
    return {
      ref: function (p) { return new Ref(p || ''); },
      goOnline: function () {}, goOffline: function () {},
    };
  };

  // firebase.database.ServerValue.TIMESTAMP is still used in a few places
  var dbNs = { ServerValue: { TIMESTAMP: { '.sv': 'timestamp' } } };
  try { firebase.database = dbNs; } catch (e) { try { Object.defineProperty(firebase, 'database', { value: dbNs }); } catch (e2) {} }

  // Admin API helper (always talks to the MAIN server): mxApi('GET', '/api/admin/status')
  window.mxApi = function (method, path, body) {
    return getToken().then(function (t) {
      return fetch(base() + path, { method: method, headers: Object.assign({ Authorization: 'Bearer ' + t }, body !== undefined ? { 'Content-Type': 'application/json' } : {}), body: body !== undefined ? JSON.stringify(body) : undefined });
    }).then(function (r) { return r.text().then(function (txt) { var j = null; try { j = txt ? JSON.parse(txt) : null; } catch (e) {} if (!r.ok) { var er = mkErr(r.status, j && j.error); er.body = j; throw er; } return j; }); });
  };

  // Admin helper (browser console): downloads a fresh backup of database.db  ->  mxBackupDownload()
  window.mxBackupDownload = function () {
    return getToken().then(function (t) {
      return fetch(base() + '/api/admin/backup', { headers: { Authorization: 'Bearer ' + t } });
    }).then(function (r) { if (!r.ok) throw new Error('backup failed ' + r.status); return r.blob(); })
      .then(function (b) { var a = document.createElement('a'); a.href = URL.createObjectURL(b); a.download = 'movex-backup.db.gz'; a.click(); });
  };
})();
