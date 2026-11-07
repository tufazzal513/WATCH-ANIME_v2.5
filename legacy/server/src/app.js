// HTTP API (Node built-in http, no frameworks).
//   GET    /api/db/<path>[?orderBy=c&limitToLast=12]
//   PUT    /api/db/<path>      body = JSON value      (set)
//   PATCH  /api/db/<path>      body = { "a/b": v }    (update, multi-path; path may be empty)
//   DELETE /api/db/<path>                              (remove)
//   GET    /api/admin/backup   (admin)  download a fresh gzip backup
//   POST   /api/admin/backup   (admin)  run the backup job now (local + Google Drive)
//   GET    /api/health
import http from 'node:http';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { openStore, clean } from './store.js';
import { canRead, checkWrite } from './rules.js';
import { createVerifier } from './auth.js';
import { createBackupManager } from './backup.js';
import { createTelegram } from './telegram.js';
import { createConfig } from './config.js';
import { createPuller } from './sync.js';
import { HttpError, bad, deny } from './util.js';

const SEG_BAD = /[.$#\[\]\/\u0000-\u001f]/;
const validSeg = (s) => s && s.length <= 400 && !SEG_BAD.test(s);

function limiter(max, windowMs) {
  const m = new Map();
  setInterval(() => { const n = Date.now(); for (const [k, v] of m) if (v.reset < n) m.delete(k); }, windowMs).unref();
  return (key) => {
    const n = Date.now();
    let e = m.get(key);
    if (!e || e.reset < n) { e = { c: 0, reset: n + windowMs }; m.set(key, e); }
    return ++e.c <= max;
  };
}

export function createApp(cfg) {
  const store = openStore(cfg.dbPath);
  const verifier = cfg.verifier || createVerifier({ projectId: cfg.projectId, devInsecure: cfg.devInsecureAuth });
  const role = cfg.role === 'worker' ? 'worker' : 'main';
  const isWorker = role === 'worker';
  const log = cfg.log || console;
  // Settings = environment variables as the starting point, overridden by whatever the admin saved in the panel.
  if (cfg.resetPanelSettings) store.setMeta('config', '{}');   // recovery switch (RESET_PANEL_SETTINGS=1 on the host)
  const conf = createConfig({ store, env: {
    ALLOWED_ORIGINS: cfg.allowedOrigins, TRUST_PROXY: cfg.trustProxy ? '1' : '', BACKUP_EVERY_HOURS: cfg.backupEveryHours, BACKUP_KEEP_LOCAL: cfg.backupKeepLocal,
    TG_BOT_TOKEN: cfg.tgToken, TG_CHAT_ID: cfg.tgChatId, TG_API_ID: cfg.tgApiId, TG_API_HASH: cfg.tgApiHash, TG_SESSION: cfg.tgSession, TG_KEEP: cfg.tgKeep,
    GDRIVE_CLIENT_ID: cfg.drive && cfg.drive.clientId, GDRIVE_CLIENT_SECRET: cfg.drive && cfg.drive.clientSecret, GDRIVE_REFRESH_TOKEN: cfg.drive && cfg.drive.refreshToken,
    GDRIVE_FOLDER_ID: cfg.drive && cfg.drive.folderId, GDRIVE_KEEP_DAYS: cfg.driveKeepDays,
  } });
  const buildTg = () => createTelegram({ token: conf.get('TG_BOT_TOKEN'), chatId: conf.get('TG_CHAT_ID'), apiId: conf.get('TG_API_ID'), apiHash: conf.get('TG_API_HASH'), session: conf.get('TG_SESSION'), keep: Number(conf.get('TG_KEEP') || 30), dir: cfg.backupDir, log });
  let tgNow = cfg.tg || buildTg();
  // the backup code keeps using this wrapper; it always points at the current Telegram settings
  const tg = { get enabled() { return tgNow.enabled; }, get mode() { return tgNow.mode; }, upload: (x) => tgNow.upload(x), latest: () => tgNow.latest(), download: (d) => tgNow.download(d) };
  conf.onChange((keys) => { if (!cfg.tg && keys.some(k => k.startsWith('TG_'))) { const old = tgNow; tgNow = buildTg(); old.close && old.close().catch(() => {}); } });
  const driveNow = () => ({ clientId: conf.get('GDRIVE_CLIENT_ID'), clientSecret: conf.get('GDRIVE_CLIENT_SECRET'), refreshToken: conf.get('GDRIVE_REFRESH_TOKEN'), folderId: conf.get('GDRIVE_FOLDER_ID') });
  const backups = createBackupManager({ store, dir: cfg.backupDir, keepLocal: () => Number(conf.get('BACKUP_KEEP_LOCAL') || 7), drive: driveNow, driveKeepDays: () => Number(conf.get('GDRIVE_KEEP_DAYS') || 30), tg, log });
  const puller = isWorker ? createPuller({ store, mainUrl: cfg.mainUrl, key: cfg.workerKey, publicUrl: cfg.publicUrl, intervalMs: (cfg.syncEverySec || 5) * 1000, log }) : null;
  const bootId = crypto.randomBytes(4).toString('hex');
  const ipLimit = limiter(cfg.ipPerMinute || 900, 60e3);
  const uidLimit = limiter(cfg.uidPerMinute || 300, 60e3);
  const anonWriteLimit = limiter(cfg.anonWritesPerMinute || 20, 60e3);
  const cache = new Map();                 // url -> { etag, raw, gz }
  const lastPersist = new Map();           // uid -> last time last_seen was saved to disk

  const corsHeaders = (req) => {
    const origins = conf.get('ALLOWED_ORIGINS').split(',').map(s => s.trim()).filter(Boolean);
    const o = req.headers.origin;
    const allow = origins.includes('*') ? '*' : (o && origins.includes(o) ? o : origins[0]);
    return {
      'Access-Control-Allow-Origin': allow,
      'Access-Control-Allow-Headers': 'Authorization, Content-Type, If-None-Match, X-Worker-Key',
      'Access-Control-Allow-Methods': 'GET, PUT, PATCH, DELETE, OPTIONS',
      'Access-Control-Expose-Headers': 'ETag',
      'Access-Control-Max-Age': '86400',
      'Vary': 'Origin, Authorization, Accept-Encoding',
      'X-Content-Type-Options': 'nosniff',
    };
  };

  const clientIp = (req) => {
    if (!isWorker && req.headers['x-worker-key'] && req.headers['x-mx-client-ip'] && store.workerByKey(String(req.headers['x-worker-key']))) return String(req.headers['x-mx-client-ip']).slice(0, 64);
    if (conf.get('TRUST_PROXY') === '1') return String(req.headers['x-forwarded-for'] || '').split(',')[0].trim() || req.socket.remoteAddress;
    return req.socket.remoteAddress;
  };

  function readBody(req, limit = 2 * 1024 * 1024) {
    return new Promise((resolve, reject) => {
      let size = 0; const chunks = [];
      req.on('data', (c) => { size += c.length; if (size > limit) { reject(new HttpError(413, 'body_too_large')); req.destroy(); } else chunks.push(c); });
      req.on('end', () => {
        if (!chunks.length) return resolve(undefined);
        try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8'))); } catch { reject(new HttpError(400, 'invalid_json')); }
      });
      req.on('error', reject);
    });
  }

  async function authenticate(req) {
    const h = req.headers.authorization;
    if (!h) return null;
    const m = /^Bearer (.+)$/.exec(h);
    if (!m) throw new HttpError(401, 'invalid_token');
    try { return await verifier.verify(m[1]); }
    catch { throw new HttpError(401, 'invalid_token'); }
  }

  const parseSegs = (pathname, prefix) => {
    const rest = pathname.slice(prefix.length).replace(/^\/+|\/+$/g, '');
    if (!rest) return [];
    const segs = rest.split('/').map(s => { try { return decodeURIComponent(s); } catch { return bad('bad_path'); } });
    if (!segs.every(validSeg)) bad('bad_path');
    return segs;
  };

  function send(req, res, status, body, extra = {}) {
    const buf = typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body);
    res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', ...corsHeaders(req), ...extra });
    res.end(buf);
  }

  function handleGet(req, res, segs, url, auth, admin) {
    if (!segs.length) deny();
    if (!canRead(segs, auth, admin)) deny();
    const orderBy = url.searchParams.get('orderBy');
    const limit = url.searchParams.get('limitToLast');
    if (orderBy && !/^[A-Za-z0-9_]{1,40}$/.test(orderBy)) bad('bad_query');
    if (limit && !/^\d{1,4}$/.test(limit)) bad('bad_query');

    // ?lite=1 on the movie list: cards only. Episode lists and stream links are left out and fetched per title when it is opened.
    const lite = url.searchParams.get('lite') === '1' && segs.length === 1 && segs[0] === 'movies';
    const key = segs.join('/') + '?' + (orderBy || '') + '&' + (limit || '') + (lite ? '&lite' : '');
    const etag = `W/"${segs[0] === 'online_users' ? bootId + '-' : ''}${store.version(segs)}"`;   // same data => same ETag on main and every worker
    const noStore = { 'Cache-Control': 'private, no-cache', ETag: etag };
    if (req.headers['if-none-match'] === etag) { res.writeHead(304, { ...corsHeaders(req), ...noStore }); return res.end(); }

    let entry = cache.get(key);
    if (!entry || entry.etag !== etag) {
      let data = store.query(segs, { orderBy, limitToLast: limit ? Number(limit) : 0 });
      if (lite && data && typeof data === 'object') {
        const out = {};
        for (const [id, m] of Object.entries(data)) {
          if (!m || typeof m !== 'object') { out[id] = m; continue; }
          const c = {};
          for (const [k, v] of Object.entries(m)) if (k !== 'seasons' && !/^server\d*$/.test(k)) c[k] = v;
          out[id] = c;
        }
        data = out;
      }
      const raw = JSON.stringify(data);
      entry = { etag, raw: Buffer.from(raw), gz: null };
      if (cache.size > 800) cache.clear();
      cache.set(key, entry);
    }
    const wantsGz = /\bgzip\b/.test(req.headers['accept-encoding'] || '') && entry.raw.length > 1024;
    if (wantsGz) { entry.gz = entry.gz || zlib.gzipSync(entry.raw); }
    res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', ...corsHeaders(req), ...noStore, ...(wantsGz ? { 'Content-Encoding': 'gzip' } : {}) });
    res.end(wantsGz ? entry.gz : entry.raw);
  }

  function buildWrites(method, segs, body) {
    if (method === 'PATCH') {
      if (!body || typeof body !== 'object' || Array.isArray(body)) bad('invalid_body');
      const w = [];
      for (const [k, v] of Object.entries(body)) {
        const sub = k.split('/').filter(Boolean);
        if (!sub.length || !sub.every(validSeg)) bad('invalid_key');
        w.push({ segs: [...segs, ...sub], value: v });
      }
      if (w.length > 500) bad('too_many_writes');
      return w;
    }
    if (!segs.length) bad('bad_path');
    return [{ segs, value: method === 'DELETE' ? null : body }];
  }

  function handleWrite(req, res, method, segs, body, auth, admin, ip) {
    const writes = buildWrites(method, segs, body);
    if (!auth && !writes.every(w => w.segs[0] === 'pwd_resets')) deny();
    if (!auth && !anonWriteLimit(ip)) throw new HttpError(429, 'rate_limited');
    const ctx = { auth, isAdmin: admin, store };
    store.transaction(() => {
      for (const w of writes) {
        const v = clean(w.value);
        checkWrite(w.segs, v, ctx);
        store.writeAt(w.segs, v);
      }
    });
    // presence heartbeat -> remember "last seen" on disk, but at most every 10 minutes per user
    for (const w of writes) {
      if (w.segs[0] === 'online_users' && w.segs[1] && auth && auth.uid === w.segs[1]) {
        const last = lastPersist.get(w.segs[1]) || 0;
        if (Date.now() - last > 10 * 60e3) { lastPersist.set(w.segs[1], Date.now()); store.touchLastSeen(w.segs[1]); }
        break;
      }
    }
    send(req, res, 200, 'null');
  }

  function readRawBody(req, limit = 2 * 1024 * 1024) {
    return new Promise((resolve, reject) => {
      let size = 0; const chunks = [];
      req.on('data', (c) => { size += c.length; if (size > limit) { reject(new HttpError(413, 'body_too_large')); req.destroy(); } else chunks.push(c); });
      req.on('end', () => resolve(chunks.length ? Buffer.concat(chunks) : undefined));
      req.on('error', reject);
    });
  }

  // WORKER: forward to the main server (writes, admin calls, live presence), then catch up so the caller sees their own change.
  async function proxy(req, res, url, ip) {
    const body = (req.method === 'GET' || req.method === 'HEAD' || req.method === 'DELETE') ? undefined : await readRawBody(req);
    const h = { 'X-Worker-Key': cfg.workerKey, 'X-MX-Client-IP': ip };
    if (req.headers.authorization) h.Authorization = req.headers.authorization;
    if (req.headers['content-type']) h['Content-Type'] = req.headers['content-type'];
    let r;
    try { r = await fetch(cfg.mainUrl.replace(/\/+$/, '') + url.pathname + url.search, { method: req.method, headers: h, body }); }
    catch { throw new HttpError(502, 'main_unreachable'); }
    const buf = Buffer.from(await r.arrayBuffer());
    if (r.ok && req.method !== 'GET') await Promise.race([puller.syncNow(), new Promise(ok => setTimeout(ok, 1500))]);
    const out = { ...corsHeaders(req), 'Content-Type': r.headers.get('content-type') || 'application/json' };
    for (const k of ['content-disposition', 'etag', 'cache-control']) if (r.headers.get(k)) out[k] = r.headers.get(k);
    res.writeHead(r.status, out); res.end(buf);
  }

  const ONLINE_MS = 75e3;
  // MAIN -> workers: "something changed, pull now". Changes within 200 ms are merged into one ping.
  if (!isWorker) {
    let t = null;
    const ping = () => {
      let targets = [];
      try { targets = store.workerTargets(); } catch { return; }   // store already closed (shutdown)
      for (const w of targets) {
        fetch(w.url.replace(/\/+$/, '') + '/api/notify', { method: 'POST', headers: { 'X-Notify': w.key_hash }, signal: AbortSignal.timeout(3000) }).catch(() => {});
      }
    };
    store.setOnChange(() => { if (!t) { t = setTimeout(() => { t = null; ping(); }, 200); t.unref?.(); } });
  }
  function handleSync(req, res, url) {   // main only: workers pull changes here
    const w = store.workerByKey(String(req.headers['x-worker-key'] || ''));
    if (!w) throw new HttpError(401, 'invalid_worker_key');
    const since = Number(url.searchParams.get('since') || 0);
    const limit = Math.min(2000, Math.max(10, Number(url.searchParams.get('limit') || 1000)));
    const wurl = String(req.headers['x-worker-url'] || '').slice(0, 200);
    store.touchWorker(w.id, /^https?:\/\//.test(wurl) ? wurl : null, since);
    const body = Buffer.from(JSON.stringify(store.changesSince(since, limit)));
    const gz = /\bgzip\b/.test(req.headers['accept-encoding'] || '') && body.length > 1024;
    res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', ...(gz ? { 'Content-Encoding': 'gzip' } : {}) });
    res.end(gz ? zlib.gzipSync(body) : body);
  }
  function handleServers(req, res) {   // public: the site asks "which worker should I read from?"
    const now = Date.now();
    const list = store.listWorkers().filter(w => w.url && now - w.last_seen < ONLINE_MS * 2 && store.seq() - w.last_seq < 5000).map(w => ({ name: w.name, url: w.url }));
    send(req, res, 200, { workers: list }, { 'Cache-Control': 'public, max-age=60' });
  }

  async function handleAdmin(req, res, url, auth, admin) {
    if (!admin) deny();
    const sub = url.pathname.replace(/^\/api\/admin\/?/, '');
    if (sub === 'status') {
      const ws = store.listWorkers().map(w => ({ id: w.id, name: w.name, url: w.url, lastSeen: w.last_seen, behind: Math.max(0, store.seq() - w.last_seq), online: Date.now() - w.last_seen < ONLINE_MS * 2 }));
      return send(req, res, 200, { role, seq: store.seq(), backup: backups.status(), workers: ws });
    }
    if (sub === 'config') {
      if (req.method === 'GET') return send(req, res, 200, { fields: conf.view() });
      if (req.method === 'PUT') {
        const b = await readBody(req);
        // safety: never let an admin lock themselves out by saving a site list that excludes the page they are using right now
        const wanted = b && b.values && b.values.ALLOWED_ORIGINS;
        const here = String(req.headers.origin || '');
        if (wanted && here) {
          const list = String(wanted).split(',').map(s => s.trim().replace(/\/+$/, '')).filter(Boolean);
          if (!list.includes('*') && !list.includes(here)) return send(req, res, 400, { error: 'invalid_settings', errors: { ALLOWED_ORIGINS: 'must include ' + here + ' (the address you are using now), otherwise you would lock yourself out' } });
        }
        const r = conf.set(b && b.values);
        return send(req, res, r.ok ? 200 : 400, r.ok ? { ok: true, changed: r.changed, fields: conf.view() } : { error: 'invalid_settings', errors: r.errors });
      }
      bad('method_not_allowed');
    }
    if (sub === 'config/test-telegram') {
      if (req.method !== 'POST') bad('method_not_allowed');
      if (!tg.enabled) return send(req, res, 200, { ok: false, message: 'Telegram is not configured yet (channel ID + bot token, or API ID/hash + session).' });
      try { const l = await tg.latest(); return send(req, res, 200, { ok: true, mode: tg.mode, message: l ? 'Connected. A pinned backup exists in the channel.' : 'Connected. The channel has no pinned backup yet (press Backup now).' }); }
      catch (e) { return send(req, res, 200, { ok: false, mode: tg.mode, message: String(e.message || e).slice(0, 300) }); }
    }
    if (sub === 'workers') {
      if (req.method === 'POST') {
        const b = await readBody(req);
        const name = String((b && b.name) || '').trim();
        if (!name) bad('name_required');
        const u = String((b && b.url) || '').trim();
        return send(req, res, 200, store.createWorker(name, /^https?:\/\//.test(u) ? u : null));
      }
      if (req.method === 'DELETE') { const id = url.searchParams.get('id') || ''; return send(req, res, 200, { deleted: store.deleteWorker(id) }); }
      bad('method_not_allowed');
    }
    if (sub !== 'backup') throw new HttpError(404, 'not_found');
    if (req.method === 'POST') {
      const r = await backups.run();
      return send(req, res, 200, { ok: true, ...r, file: undefined });
    }
    if (req.method !== 'GET') bad('method_not_allowed');
    const tmp = path.join(cfg.backupDir, `.dl-${Date.now()}.db`);
    store.backupTo(tmp);
    const gz = zlib.gzipSync(fs.readFileSync(tmp));
    fs.unlinkSync(tmp);
    res.writeHead(200, { 'Content-Type': 'application/gzip', 'Content-Disposition': 'attachment; filename="movex-backup.db.gz"', ...corsHeaders(req) });
    res.end(gz);
  }

  const server = http.createServer(async (req, res) => {
    try {
      if (req.method === 'OPTIONS') { res.writeHead(204, corsHeaders(req)); return res.end(); }
      const url = new URL(req.url, 'http://localhost');
      const ip = clientIp(req);
      if (!ipLimit(ip)) throw new HttpError(429, 'rate_limited');

      if (url.pathname === '/api/health') return send(req, res, 200, isWorker ? { ok: true, role, ...puller.status() } : { ok: true, role, seq: store.seq() });
      if (isWorker && url.pathname === '/api/notify' && req.method === 'POST') {
        const want = crypto.createHash('sha256').update(String(cfg.workerKey)).digest();
        const got = Buffer.from(String(req.headers['x-notify'] || ''), 'hex');
        if (got.length !== want.length || !crypto.timingSafeEqual(got, want)) throw new HttpError(401, 'bad_notify');
        puller.syncNow();
        return send(req, res, 200, { ok: true });
      }
      if (!isWorker && url.pathname === '/api/sync') return handleSync(req, res, url);
      if (!isWorker && url.pathname === '/api/servers') return handleServers(req, res);

      const isDb = url.pathname === '/api/db' || url.pathname.startsWith('/api/db/');
      const isAdminRoute = url.pathname === '/api/admin' || url.pathname.startsWith('/api/admin/');
      if (!isDb && !isAdminRoute) throw new HttpError(404, 'not_found');

      if (isWorker) {
        const live = /^\/api\/db\/online_users(\/|$)/.test(url.pathname);
        if (isAdminRoute || req.method !== 'GET' || live) return await proxy(req, res, url, ip);
        if (url.pathname === '/api/servers') return await proxy(req, res, url, ip);
        if (!puller.ready) throw new HttpError(503, 'warming_up');
      }

      const auth = await authenticate(req);
      if (auth && !uidLimit(auth.uid)) throw new HttpError(429, 'rate_limited');
      const admin = !!auth && store.isAdmin(auth.uid);

      if (isAdminRoute) return await handleAdmin(req, res, url, auth, admin);

      const segs = parseSegs(url.pathname, '/api/db');
      if (req.method === 'GET') return handleGet(req, res, segs, url, auth, admin);
      if (req.method === 'PUT' || req.method === 'PATCH' || req.method === 'DELETE') {
        const body = req.method === 'DELETE' ? undefined : await readBody(req);
        return handleWrite(req, res, req.method, segs, body, auth, admin, ip);
      }
      throw new HttpError(405, 'method_not_allowed');
    } catch (e) {
      const status = e instanceof HttpError ? e.status : 500;
      if (status === 500) (cfg.log || console).error('[error]', e);
      if (!res.headersSent) send(req, res, status, { error: e instanceof HttpError ? e.code : 'server_error' });
    }
  });

  return { server, store, backups, tg, puller, role, conf, close: () => new Promise((r) => { server.close(() => { try { store.close(); } catch { /* ignore */ } r(); }); server.closeAllConnections?.(); }) };
}
