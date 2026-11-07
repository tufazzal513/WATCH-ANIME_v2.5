import test, { after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createApp } from '../src/app.js';
import { restoreIfEmpty } from '../src/restore.js';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'movex-cl-'));
const quiet = { log() {}, error() {} };
const ADMIN = 'dev:admin1', U1 = 'dev:user1';
const sleep = (ms) => new Promise(r => setTimeout(r, ms));
const listen = async (app) => { await new Promise(r => app.server.listen(0, '127.0.0.1', r)); return 'http://127.0.0.1:' + app.server.address().port; };
const api = async (base, method, p, { token, body } = {}) => {
  const r = await fetch(base + p, { method, headers: { ...(token ? { Authorization: 'Bearer ' + token } : {}), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) }, body: body !== undefined ? JSON.stringify(body) : undefined });
  const t = await r.text(); return { status: r.status, json: t ? JSON.parse(t) : undefined, headers: r.headers };
};
// in-memory stand-in for the Telegram channel
function fakeTg() {
  const files = []; let pinned = null; let id = 0;
  return { enabled: true,
    async upload({ dbBuf, jsonBuf }) { const a = { file_id: 'db' + (++id), buf: dbBuf, file_size: dbBuf.length }; const b = { file_id: 'js' + (++id), buf: jsonBuf, file_size: jsonBuf.length }; files.push(a, b); pinned = { db: a, json: b, date: Math.floor(Date.now() / 1000) }; return { ok: 1 }; },
    async latest() { return pinned; },
    async download(d) { return d.buf; } };
}
const apps = [];
after(async () => { for (const a of apps) await a.close(); fs.rmSync(dir, { recursive: true, force: true }); });

test('worker replicates main, serves reads, forwards writes, follows deletes', async () => {
  const tg = fakeTg();
  const main = createApp({ dbPath: path.join(dir, 'main.db'), backupDir: path.join(dir, 'bk'), devInsecureAuth: true, log: quiet, tg }); apps.push(main);
  const mb = await listen(main);
  main.store.writeAt(['settings', 'adminUIDs', 'admin1'], true);
  main.store.writeAt(['movies', 'm1'], { title: 'One' });

  const w = await api(mb, 'POST', '/api/admin/workers', { token: ADMIN, body: { name: 'w1', url: 'https://w1.example' } });
  assert.equal(w.status, 200); assert.match(w.json.key, /^wk_/);
  assert.equal((await api(mb, 'POST', '/api/admin/workers', { token: U1, body: { name: 'x' } })).status, 403);
  assert.equal((await api(mb, 'GET', '/api/sync?since=0')).status, 401);

  const worker = createApp({ role: 'worker', mainUrl: mb, workerKey: w.json.key, syncEverySec: 600, dbPath: path.join(dir, 'w1.db'), backupDir: path.join(dir, 'wbk'), devInsecureAuth: true, log: quiet }); apps.push(worker);
  const wb = await listen(worker); main.store.touchWorker(w.json.id, wb, 0); worker.puller.start();
  for (let i = 0; i < 40 && !worker.puller.ready; i++) await sleep(50);
  assert.ok(worker.puller.ready);

  const rm = await api(mb, 'GET', '/api/db/movies', { token: U1 });
  const rw = await api(wb, 'GET', '/api/db/movies', { token: U1 });
  assert.deepEqual(rw.json, rm.json);
  assert.equal(rw.headers.get('etag'), rm.headers.get('etag'));          // identical ETag everywhere
  assert.equal((await api(wb, 'GET', '/api/db/movies')).status, 403);      // worker enforces the same rules

  // admin writes THROUGH the worker -> lands on main, visible on worker right away
  assert.equal((await api(wb, 'PUT', '/api/db/movies/m2', { token: ADMIN, body: { title: 'Two' } })).status, 200);
  assert.equal((await api(mb, 'GET', '/api/db/movies/m2/title', { token: U1 })).json, 'Two');
  assert.equal((await api(wb, 'GET', '/api/db/movies/m2/title', { token: U1 })).json, 'Two');
  assert.equal((await api(wb, 'PUT', '/api/db/movies/m3', { token: U1, body: { title: 'x' } })).status, 403);   // main's rules apply

  // delete on main reaches worker
  await api(mb, 'DELETE', '/api/db/movies/m1', { token: ADMIN });
  for (let i = 0; i < 40; i++) { if ((await api(wb, 'GET', '/api/db/movies/m1', { token: U1 })).json === null) break; await sleep(100); }
  assert.equal((await api(wb, 'GET', '/api/db/movies/m1', { token: U1 })).json, null);

  // servers list shows the worker; status shows it online
  await sleep(100);
  const st = await api(mb, 'GET', '/api/admin/status', { token: ADMIN });
  assert.equal(st.json.workers.length, 1); assert.equal(st.json.workers[0].online, true);
  assert.equal((await api(mb, 'GET', '/api/servers')).json.workers[0].url, wb);
  assert.equal((await api(wb, 'GET', '/api/health')).json.role, 'worker');
});

test('backup -> Telegram (.db + .json) -> brand-new server restores the latest', async () => {
  const tg = fakeTg();
  const main = createApp({ dbPath: path.join(dir, 'a.db'), backupDir: path.join(dir, 'abk'), devInsecureAuth: true, log: quiet, tg }); apps.push(main);
  const b = await listen(main);
  main.store.writeAt(['settings', 'adminUIDs', 'admin1'], true);
  main.store.writeAt(['movies', 'k1'], { title: 'Keep me', seasons: { S1: [{ n: 1 }] } });
  const r = await api(b, 'POST', '/api/admin/backup', { token: ADMIN });
  assert.equal(r.status, 200); assert.ok(r.json.telegram);

  // a brand-new server (empty disk)
  const p2 = path.join(dir, 'new', 'database.db');
  const res = await restoreIfEmpty({ dbPath: p2, tg, log: quiet });
  assert.equal(res.restored, true); assert.equal(res.from, 'db');
  const fresh = createApp({ dbPath: p2, backupDir: path.join(dir, 'nbk'), devInsecureAuth: true, log: quiet, tg }); apps.push(fresh);
  const fb = await listen(fresh);
  assert.equal((await api(fb, 'GET', '/api/db/movies/k1/title', { token: U1 })).json, 'Keep me');
  assert.equal(fresh.store.isAdmin('admin1'), true);

  // not empty -> never overwritten
  assert.equal((await restoreIfEmpty({ dbPath: p2, tg, log: quiet })).restored, false);

  // .json fallback works too
  const tg2 = { ...tg, latest: async () => ({ ...(await tg.latest()), db: null }) };
  const res2 = await restoreIfEmpty({ dbPath: path.join(dir, 'new2', 'database.db'), tg: tg2, log: quiet });
  assert.equal(res2.from, 'json'); assert.ok(res2.docs >= 2);
});

test('movies?lite=1 drops episode lists and links, full title still has them', async () => {
  const main = createApp({ dbPath: path.join(dir, 'lite.db'), backupDir: path.join(dir, 'lbk'), devInsecureAuth: true, log: quiet }); apps.push(main);
  const b = await listen(main);
  main.store.writeAt(['movies', 'a'], { title: 'A', posterUrl: 'p', description: 'd', contentType: 'series', seasons: { S1: [{ name: 'E1', server1: 'http://x' }] }, server1: 'http://y' });
  const lite = (await api(b, 'GET', '/api/db/movies?lite=1', { token: U1 })).json;
  assert.deepEqual(Object.keys(lite.a).sort(), ['contentType', 'description', 'posterUrl', 'title']);
  const full = (await api(b, 'GET', '/api/db/movies/a', { token: U1 })).json;
  assert.equal(full.seasons.S1[0].server1, 'http://x');
  const full2 = (await api(b, 'GET', '/api/db/movies', { token: U1 })).json;
  assert.ok(full2.a.seasons);
});

test('admin settings: validated, secrets write-only, applied live', async () => {
  const main = createApp({ dbPath: path.join(dir, 'cfg.db'), backupDir: path.join(dir, 'cbk'), devInsecureAuth: true, log: quiet, allowedOrigins: 'https://old.example' }); apps.push(main);
  const b = await listen(main);
  main.store.writeAt(['settings', 'adminUIDs', 'admin1'], true);
  assert.equal((await api(b, 'GET', '/api/admin/config', { token: U1 })).status, 403);
  const before = (await api(b, 'GET', '/api/admin/config', { token: ADMIN })).json.fields;
  assert.equal(before.find(f => f.key === 'ALLOWED_ORIGINS').source, 'env');
  assert.equal(before.find(f => f.key === 'TG_BOT_TOKEN').set, false);

  assert.equal((await api(b, 'PUT', '/api/admin/config', { token: ADMIN, body: { values: { TG_BOT_TOKEN: 'nope' } } })).status, 400);
  assert.equal((await api(b, 'PUT', '/api/admin/config', { token: ADMIN, body: { values: { PORT: '1' } } })).status, 400);   // not an editable setting

  const secret = '123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw';
  const r = await api(b, 'PUT', '/api/admin/config', { token: ADMIN, body: { values: { TG_BOT_TOKEN: secret, TG_CHAT_ID: '@mybackups', ALLOWED_ORIGINS: 'https://new.example/' } } });
  assert.equal(r.status, 200);
  const f = r.json.fields.find(x => x.key === 'TG_BOT_TOKEN');
  assert.equal(f.set, true); assert.equal(f.value, undefined); assert.ok(!JSON.stringify(r.json).includes(secret));   // the secret never comes back

  // lock-out protection: refuse a site list that excludes the page being used
  const lock = await fetch(b + '/api/admin/config', { method: 'PUT', headers: { Authorization: 'Bearer ' + ADMIN, 'Content-Type': 'application/json', Origin: 'https://me.example' }, body: JSON.stringify({ values: { ALLOWED_ORIGINS: 'https://other.example' } }) });
  assert.equal(lock.status, 400);

  // live: CORS follows the saved origin, Telegram counts as configured, nothing restarted
  const opt = await fetch(b + '/api/db/settings', { method: 'OPTIONS', headers: { Origin: 'https://new.example' } });
  assert.equal(opt.headers.get('access-control-allow-origin'), 'https://new.example');
  assert.equal((await api(b, 'GET', '/api/admin/status', { token: ADMIN })).json.backup.telegramConfigured, true);

  // a field left out keeps its value; "" clears it back to env
  await api(b, 'PUT', '/api/admin/config', { token: ADMIN, body: { values: { TG_CHAT_ID: '' } } });
  const after = (await api(b, 'GET', '/api/admin/config', { token: ADMIN })).json.fields;
  assert.equal(after.find(x => x.key === 'TG_BOT_TOKEN').set, true);
  assert.equal(after.find(x => x.key === 'TG_CHAT_ID').set, false);
  assert.equal(after.find(x => x.key === 'ALLOWED_ORIGINS').source, 'panel');

  // stored in the database => survives restart and travels with backups
  assert.ok(JSON.parse(main.store.getMeta('config')).TG_BOT_TOKEN);
  const t = await api(b, 'POST', '/api/admin/config/test-telegram', { token: ADMIN });
  assert.equal(t.json.ok, false);   // chat id cleared -> not configured
});
