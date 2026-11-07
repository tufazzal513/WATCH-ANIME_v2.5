import test, { before, after } from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createApp } from '../src/app.js';
import { createVerifier } from '../src/auth.js';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'movex-test-'));
let app, base;
const ADMIN = 'dev:admin1', U1 = 'dev:user1', U2 = 'dev:user2';

async function call(method, p, { token, body, headers = {} } = {}) {
  const r = await fetch(base + '/api/db/' + p, {
    method,
    headers: { ...(token ? { Authorization: 'Bearer ' + token } : {}), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...headers },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await r.text();
  return { status: r.status, json: text ? JSON.parse(text) : undefined, headers: r.headers };
}

before(async () => {
  app = createApp({ dbPath: path.join(dir, 'db.sqlite'), backupDir: path.join(dir, 'bk'), projectId: 'movex-test', devInsecureAuth: true, log: { log() {}, error() {} } });
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  base = 'http://127.0.0.1:' + app.server.address().port;
  app.store.writeAt(['settings', 'adminUIDs', 'admin1'], true);
  app.store.writeAt(['settings', 'appName'], 'MoveX');
});
after(async () => { await app.close(); fs.rmSync(dir, { recursive: true, force: true }); });

test('settings are public, movies need login', async () => {
  assert.equal((await call('GET', 'settings/appName')).json, 'MoveX');
  assert.equal((await call('GET', 'movies')).status, 403);
  assert.equal((await call('GET', 'movies', { token: U1 })).status, 200);
});

test('only admins write movies; arrays round-trip; push-style ids', async () => {
  const movie = { title: 'A', seasons: { S1: [{ name: 'E1', server1: 'x' }, { name: 'E2', server1: 'y' }] }, isUpcoming: true, nothing: null };
  assert.equal((await call('PUT', 'movies/-Oabc', { token: U1, body: movie })).status, 403);
  assert.equal((await call('PUT', 'movies/-Oabc', { token: ADMIN, body: movie })).status, 200);
  const got = (await call('GET', 'movies/-Oabc', { token: U1 })).json;
  assert.equal(got.title, 'A');
  assert.ok(Array.isArray(got.seasons.S1));
  assert.equal(got.seasons.S1[1].name, 'E2');
  assert.equal('nothing' in got, false);
});

test('multi-path update from root (upcoming checker) works for admin only', async () => {
  const body = { 'movies/-Oabc/isUpcoming': false };
  assert.equal((await call('PATCH', '', { token: U1, body })).status, 403);
  assert.equal((await call('PATCH', '', { token: ADMIN, body })).status, 200);
  assert.equal((await call('GET', 'movies/-Oabc/isUpcoming', { token: U1 })).json, false);
});

test('ETag / 304 and refresh after write', async () => {
  const a = await call('GET', 'movies', { token: U1 });
  const etag = a.headers.get('etag');
  assert.ok(etag);
  assert.equal((await call('GET', 'movies', { token: U1, headers: { 'If-None-Match': etag } })).status, 304);
  await call('PUT', 'movies/-Odef', { token: ADMIN, body: { title: 'B' } });
  const b = await call('GET', 'movies', { token: U1, headers: { 'If-None-Match': etag } });
  assert.equal(b.status, 200);
  assert.equal(Object.keys(b.json).length, 2);
});

test('users: own record only, status cannot be changed by user', async () => {
  const rec = { uid: 'user1', name: 'U', status: 'active', createdAt: { '.sv': 'timestamp' } };
  assert.equal((await call('PUT', 'users/user1', { token: U1, body: rec })).status, 200);
  assert.equal(typeof (await call('GET', 'users/user1', { token: U1 })).json.createdAt, 'number');
  assert.equal((await call('GET', 'users/user1', { token: U2 })).status, 403);
  assert.equal((await call('GET', 'users', { token: U1 })).status, 403);
  assert.equal((await call('GET', 'users', { token: ADMIN })).status, 200);
  assert.equal((await call('PUT', 'users/user1/status', { token: U1, body: 'frozen' })).status, 403);
  assert.equal((await call('PUT', 'users/user1', { token: U2, body: rec })).status, 403);
  assert.equal((await call('PUT', 'users/user1/status', { token: ADMIN, body: 'frozen' })).status, 200);
  assert.equal((await call('PUT', 'users/user1/status', { token: ADMIN, body: 'active' })).status, 200);
});

test('my list + progress validation', async () => {
  assert.equal((await call('PUT', 'users/user1/myList/-Oabc', { token: U1, body: { '.sv': 'timestamp' } })).status, 200);
  assert.equal((await call('PUT', 'users/user1/myList/-Odef', { token: U1, body: 'nope' })).status, 403);
  assert.equal((await call('PUT', 'users/user1/progress/-Oabc', { token: U1, body: { t: 10, d: 100, ep: null } })).status, 200);
  assert.equal((await call('PUT', 'users/user1/progress/-Odef', { token: U1, body: { t: 10 } })).status, 403);
  assert.equal((await call('DELETE', 'users/user1/myList/-Oabc', { token: U1 })).status, 200);
});

test('deleted user can remove own record only after admin marked it deleted', async () => {
  assert.equal((await call('DELETE', 'users/user1', { token: U1 })).status, 403);
  await call('PUT', 'users/user1', { token: ADMIN, body: { uid: 'user1', status: 'deleted', deletedAt: { '.sv': 'timestamp' } } });
  assert.equal((await call('DELETE', 'users/user1', { token: U1 })).status, 200);
});

test('stats counters accept only +1', async () => {
  assert.equal((await call('PUT', 'stats/-Oabc/plays', { token: U1, body: 1 })).status, 200);
  assert.equal((await call('PUT', 'stats/-Oabc/plays', { token: U1, body: 5 })).status, 403);
  assert.equal((await call('PUT', 'stats/-Oabc/plays', { token: U1, body: 2 })).status, 200);
  assert.equal((await call('PUT', 'stats/-Oabc/hack', { token: U1, body: 1 })).status, 403);
  assert.equal((await call('PUT', 'stats/-Oabc/lists', { token: U1, body: 1 })).status, 200);
  assert.equal((await call('PUT', 'stats/-Oabc/lists', { token: U1, body: 0 })).status, 200);
  assert.equal((await call('PUT', 'stats/-Oabc/lists', { token: U1, body: -1 })).status, 403);
  assert.deepEqual((await call('GET', 'stats', { token: U2 })).json, { '-Oabc': { plays: 2, lists: 0 } });
});

test('search stats: transaction-style writes + top-N query', async () => {
  for (const [k, c] of [['batman', 5], ['dune', 3], ['up', 1]]) {
    for (let i = 1; i <= c; i++) {
      const r = await call('PUT', 'searchStats/' + k, { token: U1, body: { t: k.length < 2 ? k + k : k, c: i, at: Date.now() } });
      assert.equal(r.status, 200, k + i);
    }
  }
  assert.equal((await call('PUT', 'searchStats/batman', { token: U1, body: { t: 'batman', c: 99, at: 1 } })).status, 403);
  const top = (await call('GET', 'searchStats?orderBy=c&limitToLast=2', { token: U1 })).json;
  assert.deepEqual(Object.keys(top).sort(), ['batman', 'dune']);
});

test('password-reset log works logged out, rejects non-numbers, anon cannot touch anything else', async () => {
  assert.equal((await call('PUT', 'pwd_resets/a@b_com', { body: { k1: Date.now() } })).status, 200);
  assert.equal((await call('GET', 'pwd_resets/a@b_com')).status, 200);
  assert.equal((await call('GET', 'pwd_resets')).status, 403);
  assert.equal((await call('PUT', 'pwd_resets/a@b_com', { body: { k1: 'x' } })).status, 403);
  assert.equal((await call('PUT', 'movies/x', { body: { t: 1 } })).status, 403);
  assert.equal((await call('GET', 'settings')).status, 200);
});

test('broken links + error log', async () => {
  assert.equal((await call('PUT', 'broken_links/-Oabc', { token: U1, body: { at: 1 } })).status, 200);
  assert.equal((await call('GET', 'broken_links', { token: U1 })).status, 403);
  assert.equal((await call('GET', 'broken_links', { token: ADMIN })).status, 200);
  assert.equal((await call('PUT', 'analytics/errors/e1', { token: U1, body: { msg: 'boom', ts: 1 } })).status, 200);
  assert.equal((await call('PUT', 'analytics/errors/e1', { token: U1, body: { msg: 'again' } })).status, 403);
  assert.equal(Object.keys((await call('GET', 'analytics/errors', { token: ADMIN })).json).length, 1);
});

test('presence lives in memory; admin sees it', async () => {
  assert.equal((await call('PUT', 'online_users/user1', { token: U1, body: { name: 'U', last_seen: Date.now() } })).status, 200);
  assert.equal((await call('PATCH', 'online_users/user1', { token: U1, body: { last_seen: Date.now() } })).status, 200);
  assert.equal((await call('PUT', 'online_users/user2', { token: U1, body: { name: 'x' } })).status, 403);
  assert.equal((await call('GET', 'online_users', { token: U1 })).status, 403);
  const online = (await call('GET', 'online_users', { token: ADMIN })).json;
  assert.deepEqual(Object.keys(online), ['user1']);
  assert.equal((await call('DELETE', 'online_users/user1', { token: U1 })).status, 200);
  assert.equal((await call('GET', 'online_users', { token: ADMIN })).json, null);
});

test('bad paths / bodies are rejected, not crashed', async () => {
  assert.equal((await call('GET', 'movies/a.b', { token: U1 })).status, 400);
  assert.equal((await call('PUT', 'movies/x', { token: ADMIN, body: { 'a.b': 1 } })).status, 400);
  assert.equal((await call('PUT', '', { token: ADMIN, body: {} })).status, 400);
  assert.equal((await call('GET', 'nothing', { token: ADMIN })).status, 403);
  const r = await fetch(base + '/api/db/movies/x', { method: 'PUT', headers: { Authorization: 'Bearer ' + ADMIN }, body: '{oops' });
  assert.equal(r.status, 400);
  assert.equal((await call('GET', 'movies', { token: 'garbage' })).status, 401);
});

test('backup download is admin-only and a valid database', async () => {
  const bad = await fetch(base + '/api/admin/backup', { headers: { Authorization: 'Bearer ' + U1 } });
  assert.equal(bad.status, 403);
  const ok = await fetch(base + '/api/admin/backup', { headers: { Authorization: 'Bearer ' + ADMIN } });
  assert.equal(ok.status, 200);
  const buf = (await import('node:zlib')).gunzipSync(Buffer.from(await ok.arrayBuffer()));
  assert.equal(buf.subarray(0, 15).toString(), 'SQLite format 3');
  const run = await fetch(base + '/api/admin/backup', { method: 'POST', headers: { Authorization: 'Bearer ' + ADMIN } });
  assert.equal(run.status, 200);
  assert.ok(fs.readdirSync(path.join(dir, 'bk')).some(f => f.endsWith('.db.gz')));
});

test('real Firebase-style RS256 token verification (local keypair)', async () => {
  const { publicKey, privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
  const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', alg: 'RS256', use: 'sig' };
  const verifier = createVerifier({ projectId: 'movex-test', fetchKeys: async () => ({ jwk: { keys: [jwk] }, ttl: 3600e3 }) });
  const mk = (claims, key = privateKey, kid = 'k1') => {
    const now = Math.floor(Date.now() / 1000);
    const h = Buffer.from(JSON.stringify({ alg: 'RS256', kid, typ: 'JWT' })).toString('base64url');
    const p = Buffer.from(JSON.stringify({ iss: 'https://securetoken.google.com/movex-test', aud: 'movex-test', sub: 'abc', iat: now - 5, exp: now + 3600, ...claims })).toString('base64url');
    return `${h}.${p}.${crypto.sign('RSA-SHA256', Buffer.from(h + '.' + p), key).toString('base64url')}`;
  };
  assert.equal((await verifier.verify(mk({}))).uid, 'abc');
  await assert.rejects(verifier.verify(mk({ aud: 'other' })));
  await assert.rejects(verifier.verify(mk({ iss: 'https://evil' })));
  await assert.rejects(verifier.verify(mk({ exp: 1 })));
  await assert.rejects(verifier.verify(mk({ sub: '' })));
  await assert.rejects(verifier.verify(mk({}, crypto.generateKeyPairSync('rsa', { modulusLength: 2048 }).privateKey)));
  await assert.rejects(verifier.verify('dev:admin1'));   // dev tokens are refused unless DEV_INSECURE_AUTH=1
});
