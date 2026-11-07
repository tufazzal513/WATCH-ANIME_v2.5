/* MoveX v1 API client. All writes go to the main server; catalog reads may use a worker.
 * Private responses are never persisted. A 401 refreshes the Firebase ID token once.
 */
(function (w) {
  'use strict';
  if (!w.MX) throw new Error('MoveX config must load before api-client.js');
  var main = w.MX.apiBase || w.location.origin;
  var workerBase = null;
  var workerUntil = 0;
  var serversPromise = null;

  function url(root, path) { return root.replace(/\/+$/, '') + path; }
  function auth() {
    try { return w.firebase && w.firebase.auth ? w.firebase.auth() : null; } catch (_) { return null; }
  }
  function token(force) {
    var a = auth(), user = a && a.currentUser;
    return user ? user.getIdToken(!!force) : Promise.resolve(null);
  }
  function makeError(response, body) {
    var e = new Error(body && (body.message || body.error) || ('HTTP ' + response.status));
    e.status = response.status;
    e.code = body && body.error || (response.status === 401 ? 'unauthorized' : 'http_error');
    e.body = body;
    return e;
  }
  function readJSON(response) {
    if (response.status === 204) return Promise.resolve(null);
    return response.text().then(function (raw) {
      var body = null;
      if (raw) { try { body = JSON.parse(raw); } catch (_) { throw new Error('MoveX API returned invalid JSON'); } }
      if (!response.ok) throw makeError(response, body);
      return body;
    });
  }
  function discoverWorkers() {
    if (serversPromise) return serversPromise;
    serversPromise = fetch(url(main, '/api/v1/servers'), { headers: { Accept: 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : { workers: [] }; })
      .then(function (data) {
        var workers = Array.isArray(data && data.servers) ? data.servers : [];
        if (!workers.length) return null;
        return Promise.all(workers.map(function (worker) {
          if (!worker || typeof worker.url !== 'string' || !/^https:\/\//i.test(worker.url)) return Promise.resolve(null);
          var root = worker.url.replace(/\/+$/, '');
          var controller = typeof AbortController === 'function' ? new AbortController() : null;
          var timer = controller ? setTimeout(function () { controller.abort(); }, w.MX.workerHealthTimeoutMs) : null;
          return fetch(url(root, '/api/v1/health'), controller ? { signal: controller.signal } : {})
            .then(function (r) { return r.ok ? { root: root, latency: 1 } : null; })
            .catch(function () { return null; })
            .then(function (result) { if (timer) clearTimeout(timer); return result; });
        })).then(function (items) {
          items = items.filter(Boolean);
          return items.length ? items[0].root : null;
        });
      }).catch(function () { return null; })
      .then(function (root) { workerBase = root; workerUntil = Date.now() + 300000; return root; });
    return serversPromise;
  }
  function chooseReadRoot() {
    if (Date.now() < workerUntil) return Promise.resolve(workerBase || main);
    return discoverWorkers().then(function (root) { return root || main; });
  }
  function request(method, path, body, options, retried) {
    options = options || {};
    var isRead = method === 'GET';
    return token(!!retried).then(function (idToken) {
      var headers = { Accept: 'application/json' };
      if (idToken) headers.Authorization = 'Bearer ' + idToken;
      if (body !== undefined) headers['Content-Type'] = 'application/json';
      var rootPromise = isRead && options.worker !== false ? chooseReadRoot() : Promise.resolve(main);
      return rootPromise.then(function (root) {
        var controller = typeof AbortController === 'function' ? new AbortController() : null;
        var timer = controller ? setTimeout(function () { controller.abort(); }, options.timeout || w.MX.requestTimeoutMs) : null;
        return fetch(url(root, path), {
          method: method,
          headers: headers,
          body: body === undefined ? undefined : JSON.stringify(body),
          cache: 'no-store',
          signal: controller ? controller.signal : undefined
        }).then(function (response) {
          if (timer) clearTimeout(timer);
          if (isRead && root !== main && response.status >= 502) {
            workerUntil = 0; workerBase = null; serversPromise = null;
            return fetch(url(main, path), { method: method, headers: headers, cache: 'no-store' }).then(function (fallback) {
              if (fallback.status === 401 && !retried && auth() && auth().currentUser) return request(method, path, body, options, true);
              return readJSON(fallback);
            });
          }
          if (response.status === 401 && !retried && auth() && auth().currentUser) return request(method, path, body, options, true);
          return readJSON(response);
        }).catch(function (error) {
          if (timer) clearTimeout(timer);
          if (isRead && root !== main) {
            workerUntil = 0; workerBase = null; serversPromise = null;
            return fetch(url(main, path), { method: method, headers: headers, cache: 'no-store' }).then(readJSON);
          }
          throw error;
        });
      });
    });
  }
  function queryMovies(params) {
    var items = [];
    var baseParams = Object.assign({}, params || {});
    function page(offset) {
      var pageParams = Object.assign({}, baseParams, { limit: 100, offset: offset });
      return request('GET', '/api/v1/movies' + query(pageParams), undefined, { worker: true })
        .then(function (data) {
          var batch = Array.isArray(data && data.items) ? data.items : [];
          items = items.concat(batch);
          return data && data.hasMore && batch.length ? page(offset + batch.length) : items;
        });
    }
    return page(0);
  }
  function enc(value) { return encodeURIComponent(String(value)); }
  function query(params) {
    var out = [];
    Object.keys(params || {}).forEach(function (key) {
      var value = params[key];
      if (value !== undefined && value !== null && value !== '') out.push(enc(key) + '=' + enc(value));
    });
    return out.length ? '?' + out.join('&') : '';
  }
  var api = {
    request: request,
    health: function () { return request('GET', '/api/v1/health', undefined, { worker: false }); },
    settings: function () { return request('GET', '/api/v1/settings/public', undefined, { worker: false }); },
    movies: function (params) { return request('GET', '/api/v1/movies' + query(params)); },
    allMovies: function () { return queryMovies({}); },
    queryMovies: function (params) { return queryMovies(params || {}); },
    movie: function (id) { return request('GET', '/api/v1/movies/' + enc(id)); },
    viewMovie: function (id) { return request('POST', '/api/v1/movies/' + enc(id) + '/view', {}); },
    categories: function () { return request('GET', '/api/v1/categories'); },
    catalogFacets: function () { return request('GET', '/api/v1/catalog/facets'); },
    banners: function () { return request('GET', '/api/v1/banners'); },
    socialLinks: function () { return request('GET', '/api/v1/social-links'); },
    notifications: function (params) { return request('GET', '/api/v1/notifications' + query(params)); },
    searchSuggest: function (q) { return request('GET', '/api/v1/search/suggest' + query({ q: q }), undefined, { worker: true }); },
    movieSuggestions: function (q, limit) { return request('GET', '/api/v1/movies' + query({ q: q, sort: 'relevance', limit: limit || 5 }), undefined, { worker: true }); },
    searchHit: function (term) { return request('POST', '/api/v1/search/hit', { term: term }, { worker: false }); },
    me: function () { return request('GET', '/api/v1/me', undefined, { worker: false }); },
    updateMe: function (data) { return request('PUT', '/api/v1/me', data, { worker: false }); },
    myList: function () { return request('GET', '/api/v1/me/list', undefined, { worker: false }); },
    addToList: function (id) { return request('PUT', '/api/v1/me/list/' + enc(id), {}, { worker: false }); },
    removeFromList: function (id) { return request('DELETE', '/api/v1/me/list/' + enc(id), undefined, { worker: false }); },
    progress: function () { return request('GET', '/api/v1/me/progress', undefined, { worker: false }); },
    setProgress: function (id, data) { return request('PUT', '/api/v1/me/progress/' + enc(id), data, { worker: false }); },
    deleteProgress: function (id) { return request('DELETE', '/api/v1/me/progress/' + enc(id), undefined, { worker: false }); },
    presence: function (name) { return request('POST', '/api/v1/presence', { name: name }, { worker: false }); },
    leavePresence: function () { return request('DELETE', '/api/v1/presence', undefined, { worker: false }); },
    brokenLink: function (data) { return request('POST', '/api/v1/broken-links', data, { worker: false }); },
    reportError: function (data) { return request('POST', '/api/v1/errors', data, { worker: false }); },
    passwordReset: function (email) { return request('POST', '/api/v1/auth/password-reset', { email: email }, { worker: false }); },
    servers: function () { serversPromise = null; return discoverWorkers(); }
  };
  w.MX_API = Object.freeze(api);
})(window);
