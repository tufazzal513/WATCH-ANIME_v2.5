const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
function browser(files, overrides = {}) {
  const calls = [];
  const user = { getIdToken: async force => force ? 'fresh-token' : 'old-token' };
  const context = {
    console,
    URL,
    Date,
    Promise,
    Object,
    Array,
    String,
    Error,
    AbortController,
    setTimeout,
    clearTimeout,
    location: { origin: 'https://main.example' },
    MX_CONFIG: { apiBase: 'https://main.example' },
    firebase: { auth: () => ({ currentUser: user }) },
    fetch: async (url, options = {}) => {
      calls.push({ url, options });
      if (overrides.fetch) return overrides.fetch(url, options, calls.length);
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
  };
  context.window = context;
  vm.createContext(context);
  for (const file of files) vm.runInContext(fs.readFileSync(path.join(root, file), 'utf8'), context, { filename: file });
  return { context, calls };
}

test('runtime config accepts HTTPS API base and rejects insecure remote HTTP', () => {
  const ok = browser(['assets/js/shared/config.js']);
  assert.equal(ok.context.MX.apiBase, 'https://main.example');
  const badContext = { window: { MX_CONFIG: { apiBase: 'http://api.example' } } };
  vm.createContext(badContext);
  assert.throws(() => vm.runInContext(fs.readFileSync(path.join(root, 'assets/js/shared/config.js'), 'utf8'), badContext), /must use HTTPS/);
});

test('catalog request uses v1 endpoint and Firebase bearer token', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js']);
  await b.context.MX_API.movies({ limit: 20, q: 'blue lock' });
  assert.equal(b.calls[0].url, 'https://main.example/api/v1/servers');
  assert.equal(b.calls[1].url, 'https://main.example/api/v1/movies?limit=20&q=blue%20lock');
  assert.equal(b.calls[1].options.headers.Authorization, 'Bearer old-token');
  assert.equal(b.calls[1].options.cache, 'no-store');
});

test('401 refreshes the Firebase token once', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js'], {
    fetch: async (_url, _options, count) => new Response(JSON.stringify({ error: 'unauthorized' }), { status: count === 1 ? 401 : 200 })
  });
  await b.context.MX_API.me();
  assert.equal(b.calls.length, 2);
  assert.equal(b.calls[1].options.headers.Authorization, 'Bearer fresh-token');
});

test('writes are sent to main and private calls disable worker routing', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js']);
  await b.context.MX_API.addToList('movie-1');
  assert.equal(b.calls[0].url, 'https://main.example/api/v1/me/list/movie-1');
  assert.equal(b.calls[0].options.method, 'PUT');
});


test('allMovies follows backend pagination until hasMore is false', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js'], {
    fetch: async (url) => {
      if (url.endsWith('/api/v1/servers')) return new Response(JSON.stringify({ servers: [] }), { status: 200 });
      const u = new URL(url);
      const offset = Number(u.searchParams.get('offset'));
      const data = offset === 0
        ? { items: Array.from({ length: 100 }, (_, i) => ({ id: 'm' + i })), hasMore: true }
        : { items: [{ id: 'm100' }], hasMore: false };
      return new Response(JSON.stringify(data), { status: 200 });
    }
  });
  const items = await b.context.MX_API.allMovies();
  assert.equal(items.length, 101);
  assert.equal(items[100].id, 'm100');
  assert.ok(b.calls.some(call => call.url.includes('offset=100')));
});

test('search hit uses the backend term field and writes only to main', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js']);
  await b.context.MX_API.searchHit('blue lock');
  assert.equal(b.calls[0].url, 'https://main.example/api/v1/search/hit');
  assert.equal(b.calls[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(b.calls[0].options.body), { term: 'blue lock' });
});


test('user bootstrap no longer loads the Realtime Database adapter', () => {
  const bootstrap = fs.readFileSync(path.join(root, 'assets/js/user/bootstrap.js'), 'utf8');
  assert.doesNotMatch(bootstrap, /shared\/db-client\.js/);
  const activeUserJS = fs.readdirSync(path.join(root, 'assets/js/user'), { recursive: true })
    .filter(name => String(name).endsWith('.js'));
  for (const name of activeUserJS) {
    const source = fs.readFileSync(path.join(root, 'assets/js/user', name), 'utf8');
    assert.doesNotMatch(source, /\bdb\.ref\s*\(|firebase\.database\s*\(/, `${name} still uses Realtime Database`);
  }
});

test('queryMovies sends search to the Go catalog API and follows result pagination', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js'], {
    fetch: async (url) => {
      if (url.endsWith('/api/v1/servers')) return new Response(JSON.stringify({ servers: [] }), { status: 200 });
      const u = new URL(url);
      assert.equal(u.pathname, '/api/v1/movies');
      assert.equal(u.searchParams.get('q'), 'blue lock');
      assert.equal(u.searchParams.get('sort'), 'relevance');
      const offset = Number(u.searchParams.get('offset'));
      return new Response(JSON.stringify(offset === 0
        ? { items: Array.from({ length: 100 }, (_, i) => ({ id: 's' + i })), hasMore: true }
        : { items: [{ id: 's100' }], hasMore: false }), { status: 200 });
    }
  });
  const items = await b.context.MX_API.queryMovies({ q: 'blue lock', sort: 'relevance' });
  assert.equal(items.length, 101);
  assert.equal(items[100].id, 's100');
  assert.ok(b.calls.some(call => call.url.includes('offset=100')));
});

test('queryMovies preserves every server-side browse filter across pagination', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js'], {
    fetch: async (url) => {
      if (url.endsWith('/api/v1/servers')) return new Response(JSON.stringify({ servers: [] }), { status: 200 });
      const u = new URL(url);
      assert.equal(u.pathname, '/api/v1/movies');
      assert.equal(u.searchParams.get('category'), 'Action');
      assert.equal(u.searchParams.get('industry'), 'Japanese');
      assert.equal(u.searchParams.get('language'), 'Hindi');
      assert.equal(u.searchParams.get('type'), 'Anime');
      assert.equal(u.searchParams.get('year'), '2024');
      assert.equal(u.searchParams.get('contentType'), 'series');
      assert.equal(u.searchParams.get('upcoming'), 'no');
      assert.equal(u.searchParams.get('sort'), 'rating');
      return new Response(JSON.stringify({ items: [{ id: 'm1' }], hasMore: false }), { status: 200 });
    }
  });
  const items = await b.context.MX_API.queryMovies({
    category: 'Action', industry: 'Japanese', language: 'Hindi', type: 'Anime',
    year: 2024, contentType: 'series', upcoming: 'no', sort: 'rating'
  });
  assert.equal(items.length, 1);
  assert.equal(items[0].id, 'm1');
});

test('browse page delegates filters and sorting to the Go catalog API', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/pages/category-movies.js'), 'utf8');
  assert.match(source, /MX_API\.queryMovies\(params\)/);
  assert.match(source, /contentType: browse\.tab === 'movies' \? 'movie'/);
  assert.match(source, /upcoming: browse\.upcoming/);
  assert.match(source, /suggested: 'hot', new: 'new', top: 'rating', az: 'title'/);
  assert.doesNotMatch(source, /const browseMatches\s*=\s*\(\)\s*=>\s*mxList\(\)\.filter/);
});

test('Explore facets use the authenticated server endpoint', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js']);
  await b.context.MX_API.catalogFacets();
  assert.equal(b.calls[0].url, 'https://main.example/api/v1/servers');
  assert.equal(b.calls[1].url, 'https://main.example/api/v1/catalog/facets');
  assert.equal(b.calls[1].options.headers.Authorization, 'Bearer old-token');
});

test('autocomplete title suggestions request a small relevance-ranked server page', async () => {
  const b = browser(['assets/js/shared/config.js', 'assets/js/shared/api-client.js']);
  await b.context.MX_API.movieSuggestions('blue lock', 5);
  const call = b.calls.find(item => item.url.includes('/api/v1/movies?'));
  assert.ok(call);
  const u = new URL(call.url);
  assert.equal(u.searchParams.get('q'), 'blue lock');
  assert.equal(u.searchParams.get('sort'), 'relevance');
  assert.equal(u.searchParams.get('limit'), '5');
});

test('Explore page no longer calculates facet counts from the local full catalog', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/pages/categories.js'), 'utf8');
  assert.match(source, /MX_API\.catalogFacets\(\)/);
  assert.doesNotMatch(source, /mxList\(\)\.map/);
});

test('search autocomplete uses server title suggestions and rejects stale responses', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/pages/search.js'), 'utf8');
  assert.match(source, /MX_API\.movieSuggestions\(term, 5\)/);
  assert.match(source, /suggestionRequestID/);
  assert.match(source, /searchInput\.value\.trim\(\) !== term/);
});

test('movie detail API contract exposes typed stream and download resources', () => {
  const backend = fs.readFileSync(path.join(root, '../backend/internal/server/api_catalog.go'), 'utf8');
  const player = fs.readFileSync(path.join(root, 'assets/js/user/pages/watch-player.js'), 'utf8');
  assert.match(backend, /type linkDTO struct[\s\S]*Kind\s+string\s+`json:"kind"`[\s\S]*URL\s+string\s+`json:"url"`/);
  assert.match(backend, /Links\s+\[\]linkDTO\s+`json:"links"`/);
  assert.match(player, /link\.kind === 'download'/);
  assert.match(player, /link\.kind === 'stream'/);
  assert.match(player, /target="_blank" rel="noopener noreferrer"/);
});

test('player download actions reject unsafe URLs and do not treat download-only links as streams', () => {
  const player = fs.readFileSync(path.join(root, 'assets/js/user/pages/watch-player.js'), 'utf8');
  assert.match(player, /const safeMediaURL = \(value\)/);
  assert.match(player, /new URL\(raw\)/);
  assert.match(player, /!u\.username && !u\.password/);
  assert.match(player, /if \(safeMediaURL\(o\.server1\)\) return o\.server1/);
  assert.match(player, /o\.links\.filter\(link => link && link\.kind === 'stream'/);
  assert.match(player, /item\.links\.filter\(link => link && link\.kind === 'download'/);
  assert.match(player, /Saving behavior depends on the file host and your browser/);
});

test('notifications and social links escape untrusted content and reject unsafe protocols', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/pages/notifications.js'), 'utf8');
  assert.match(source, /notificationSafeURL/);
  assert.match(source, /u\.protocol === 'https:' \|\| u\.protocol === 'http:'/);
  assert.match(source, /esc\(item\.title\)/);
  assert.match(source, /esc\(item\.message\)/);
  assert.match(source, /esc\(item\.name\)/);
  assert.match(source, /rel="noopener noreferrer"/);
  assert.match(source, /Date unavailable/);
});


test('movie detail loading ignores stale responses and normalizes optional resource arrays', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/pages/watch-player.js'), 'utf8');
  assert.match(source, /let movieDetailsRequestID = 0/);
  assert.match(source, /const requestID = \+\+movieDetailsRequestID/);
  assert.match(source, /requestID !== movieDetailsRequestID \|\| !allMovies\[movieId\]/);
  assert.match(source, /requestID === movieDetailsRequestID/);
  assert.match(source, /Array\.isArray\(full\.seasons\) \? full\.seasons : \[\]/);
  assert.match(source, /Array\.isArray\(episode\.links\) \? episode\.links : \[\]/);
  assert.match(source, /Array\.isArray\(full\.links\) \? full\.links : \[\]/);
});

test('auth state transitions ignore stale account initialization and status responses', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/core/auth.js'), 'utf8');
  assert.match(source, /let authTransitionID = 0/);
  assert.match(source, /function isCurrentAuthUser\(user, transitionID\)/);
  assert.match(source, /const transitionID = \+\+authTransitionID/);
  assert.match(source, /if \(!isCurrentAuthUser\(user, transitionID\)\) return;/);
  assert.match(source, /transitionID === authTransitionID && document\.visibilityState === 'visible'/);
  assert.match(source, /window\.stopUserDataRefresh\(\)/);
  assert.match(source, /localStorage\.removeItem\('last_movie_id'\)/);
});

test('catalog refreshes cannot let a previous account overwrite the current session', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/core/user-data.js'), 'utf8');
  assert.match(source, /function currentUID\(\)/);
  assert.match(source, /const requestID = \+\+catalogRequestID/);
  assert.match(source, /requestID !== catalogRequestID \|\| currentUID\(\) !== uid/);
  assert.match(source, /window\.stopUserDataRefresh = function \(\)/);
  assert.match(source, /catalogRequestID\+\+/);
  assert.match(source, /allMovies = \{\}/);
});

test('private My List and progress responses are discarded after an account switch', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/features/user-lists.js'), 'utf8');
  assert.match(source, /const requestUID = _listUid/);
  assert.match(source, /_listUid !== requestUID \|\| !auth\.currentUser \|\| auth\.currentUser\.uid !== requestUID/);
});
