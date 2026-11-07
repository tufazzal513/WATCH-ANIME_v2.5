// SQLite-backed JSON tree store that behaves like a small Firebase Realtime Database.
//
// Layout: one table `docs(col, id, json)`.  The first path segment is the "collection"
// (movies, users, settings ...) and the second is the document id; everything deeper lives
// inside the document JSON.  (Special case: analytics/errors/<id> is its own collection so
// the error log never becomes one giant document.)
import { DatabaseSync } from 'node:sqlite';
import fs from 'node:fs';
import crypto from 'node:crypto';
import path from 'node:path';
import { HttpError, bad } from './util.js';

export const ERR_COL = 'analytics/errors';
const KEY_BAD = /[.$#\[\]\/\u0000-\u001f]/;

export function colInfo(segs) {
  return (segs[0] === 'analytics' && segs[1] === 'errors') ? { col: ERR_COL, n: 2 } : { col: segs[0], n: 1 };
}

// Normalise a value for storage: drop nulls/empty objects, arrays -> objects, resolve server timestamps.
export function clean(v, depth = 0) {
  if (v === null || v === undefined) return undefined;
  if (depth > 32) bad('too_deep');
  if (Array.isArray(v)) { const o = {}; v.forEach((x, i) => { o[i] = x; }); return clean(o, depth); }
  if (typeof v === 'object') {
    if (v['.sv'] === 'timestamp') return Date.now();
    const out = {};
    for (const [k, x] of Object.entries(v)) {
      if (!k || KEY_BAD.test(k) || k.length > 400) bad('invalid_key');
      const c = clean(x, depth + 1);
      if (c !== undefined) out[k] = c;
    }
    return Object.keys(out).length ? out : undefined;
  }
  if (typeof v === 'number' && !Number.isFinite(v)) return undefined;
  if (typeof v === 'string' && v.length > 500000) bad('value_too_large');
  return v;
}

// Pure helper: set `value` (undefined = delete) at `rest` inside `cur`.
export function setIn(cur, rest, value) {
  if (!rest.length) return value;
  const obj = (cur && typeof cur === 'object') ? { ...cur } : {};
  const child = setIn(obj[rest[0]], rest.slice(1), value);
  if (child === undefined) delete obj[rest[0]]; else obj[rest[0]] = child;
  return Object.keys(obj).length ? obj : undefined;
}
export function getIn(cur, rest) {
  for (const k of rest) { if (cur === null || typeof cur !== 'object') return undefined; cur = cur[k]; }
  return cur;
}

// Firebase-style output: objects whose keys are mostly 0..n become arrays.
export function toOut(v) {
  if (v === null || v === undefined || typeof v !== 'object') return v === undefined ? null : v;
  const keys = Object.keys(v);
  const out = {};
  for (const k of keys) out[k] = toOut(v[k]);
  if (keys.length && keys.every(k => /^(0|[1-9]\d*)$/.test(k))) {
    const max = Math.max(...keys.map(Number));
    if (2 * keys.length > max + 1) {
      const arr = [];
      for (let i = 0; i <= max; i++) arr.push(out[i] === undefined ? null : out[i]);
      return arr;
    }
  }
  return out;
}

export function openStore(file) {
  if (file !== ':memory:') fs.mkdirSync(path.dirname(path.resolve(file)), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL; PRAGMA busy_timeout = 5000;');
  db.exec(`CREATE TABLE IF NOT EXISTS docs (
    col TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL, updated INTEGER NOT NULL,
    PRIMARY KEY (col, id));`);
  try { db.exec('ALTER TABLE docs ADD COLUMN seq INTEGER NOT NULL DEFAULT 0'); } catch { /* already there */ }
  db.exec(`
    CREATE INDEX IF NOT EXISTS docs_seq ON docs(seq);
    CREATE TABLE IF NOT EXISTS tomb (col TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL);
    CREATE INDEX IF NOT EXISTS tomb_seq ON tomb(seq);
    CREATE TABLE IF NOT EXISTS colver (col TEXT PRIMARY KEY, seq INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT);
    CREATE TABLE IF NOT EXISTS workers (id TEXT PRIMARY KEY, name TEXT NOT NULL, key_hash TEXT NOT NULL UNIQUE,
      url TEXT, created INTEGER, last_seen INTEGER DEFAULT 0, last_seq INTEGER DEFAULT 0);
    UPDATE docs SET seq = rowid WHERE seq = 0;`);

  const q = {
    get: db.prepare('SELECT json FROM docs WHERE col = ? AND id = ?'),
    all: db.prepare('SELECT id, json FROM docs WHERE col = ?'),
    ids: db.prepare('SELECT id FROM docs WHERE col = ?'),
    put: db.prepare('INSERT INTO docs (col, id, json, updated, seq) VALUES (?, ?, ?, ?, ?) ON CONFLICT(col, id) DO UPDATE SET json = excluded.json, updated = excluded.updated, seq = excluded.seq'),
    del: db.prepare('DELETE FROM docs WHERE col = ? AND id = ?'),
    tomb: db.prepare('INSERT INTO tomb (col, id, seq) VALUES (?, ?, ?)'),
    excess: db.prepare('SELECT id FROM docs WHERE col = ? ORDER BY updated DESC, rowid DESC LIMIT -1 OFFSET ?'),
    setVer: db.prepare('INSERT INTO colver (col, seq) VALUES (?, ?) ON CONFLICT(col) DO UPDATE SET seq = excluded.seq'),
    getMeta: db.prepare('SELECT v FROM meta WHERE k = ?'),
    setMeta: db.prepare('INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v'),
  };
  const getMeta = (k) => { const r = q.getMeta.get(k); return r ? r.v : null; };
  const setMeta = (k, v) => { q.setMeta.run(k, String(v)); };
  const one = (sql) => { const r = db.prepare(sql).get(); return r ? Number(Object.values(r)[0]) || 0 : 0; };

  // Global change counter. Every write gets the next number; replicas (workers) pull "everything after N".
  let curSeq = Math.max(one('SELECT MAX(seq) FROM docs'), one('SELECT MAX(seq) FROM tomb'), one('SELECT MAX(seq) FROM colver'));
  const tick = () => ++curSeq;
  const colSeq = new Map(db.prepare('SELECT col, seq FROM colver').all().map(r => [r.col, Number(r.seq)]));
  for (const r of db.prepare('SELECT col, MAX(seq) AS s FROM docs GROUP BY col').all()) if (!colSeq.has(r.col)) colSeq.set(r.col, Number(r.s));
  if (!getMeta('epoch')) setMeta('epoch', crypto.randomBytes(6).toString('hex'));

  const ver = new Map();   // online_users only (memory)
  const online = new Map(); // uid -> value (presence lives in memory, never on disk)
  const ONLINE_TTL = 10 * 60 * 1000;
  let adminCache = null;
  let onChange = null;
  const bump = (c) => {
    if (c === 'online_users') { ver.set(c, (ver.get(c) || 0) + 1); return; }
    const s = tick(); colSeq.set(c, s); q.setVer.run(c, s);
    if (c === 'settings') adminCache = null;
    if (onChange) onChange(c);
  };

  const getDoc = (col, id) => { const r = q.get.get(col, id); return r ? JSON.parse(r.json) : undefined; };
  const putDoc = (col, id, val) => {
    const s = tick();
    if (val === undefined) { if (q.del.run(col, id).changes) q.tomb.run(col, id, s); }
    else q.put.run(col, id, JSON.stringify(val), Date.now(), s);
  };
  const getCol = (col) => {
    const out = {};
    for (const r of q.all.all(col)) out[r.id] = JSON.parse(r.json);
    return Object.keys(out).length ? out : undefined;
  };
  const pruneOnline = () => {
    const now = Date.now();
    for (const [k, v] of online) if (!v.last_seen || now - v.last_seen > ONLINE_TTL) online.delete(k);
  };

  function readRaw(segs) {
    if (!segs.length) throw new HttpError(400, 'bad_path');
    if (segs[0] === 'online_users') {
      pruneOnline();
      if (segs.length === 1) return online.size ? Object.fromEntries(online) : undefined;
      return getIn(online.get(segs[1]), segs.slice(2));
    }
    const { col, n } = colInfo(segs);
    if (segs.length === n) return getCol(col);
    return getIn(getDoc(col, segs[n]), segs.slice(n + 1));
  }

  function writeAt(segs, value) {   // value is already clean() (undefined = delete)
    if (!segs.length) throw new HttpError(400, 'bad_path');
    if (segs[0] === 'online_users') {
      if (segs.length === 1) { if (value === undefined) online.clear(); else { online.clear(); for (const [k, v] of Object.entries(value)) online.set(k, v); } }
      else {
        const next = setIn(online.get(segs[1]), segs.slice(2), value);
        if (next === undefined) online.delete(segs[1]); else online.set(segs[1], next);
      }
      bump('online_users');
      return;
    }
    const { col, n } = colInfo(segs);
    if (segs.length === n) {
      for (const r of q.ids.all(col)) putDoc(col, r.id, undefined);
      if (value && typeof value === 'object') for (const [id, doc] of Object.entries(value)) putDoc(col, id, doc);
    } else {
      const id = segs[n];
      putDoc(col, id, setIn(getDoc(col, id), segs.slice(n + 1), value));
    }
    if (col === ERR_COL) for (const r of q.excess.all(col, 500)) putDoc(col, r.id, undefined);
    bump(col);
  }

  function transaction(fn) {
    db.exec('BEGIN IMMEDIATE');
    try { const r = fn(); db.exec('COMMIT'); return r; }
    catch (e) { try { db.exec('ROLLBACK'); } catch { /* ignore */ } throw e; }
  }

  function query(segs, opts = {}) {   // opts: { orderBy, limitToLast }
    let v = readRaw(segs);
    if (v && typeof v === 'object' && (opts.orderBy || opts.limitToLast)) {
      let entries = Object.entries(v);
      if (opts.orderBy) {
        const k = opts.orderBy;
        const val = (e) => (e[1] && typeof e[1] === 'object') ? e[1][k] : undefined;
        entries.sort((a, b) => {
          const x = val(a), y = val(b);
          if (x === undefined && y === undefined) return 0;
          if (x === undefined) return -1;
          if (y === undefined) return 1;
          return x < y ? -1 : x > y ? 1 : 0;
        });
      }
      if (opts.limitToLast) entries = entries.slice(-opts.limitToLast);
      v = Object.fromEntries(entries);
    }
    return toOut(v);
  }

  function isAdmin(uid) {
    if (!uid) return false;
    if (!adminCache) adminCache = new Set(Object.entries(getDoc('settings', 'adminUIDs') || {}).filter(([, v]) => v === true).map(([k]) => k));
    return adminCache.has(uid);
  }

  function touchLastSeen(uid) {
    const u = getDoc('users', uid);
    if (!u || u.status === 'deleted') return;
    u.last_seen = Date.now();
    putDoc('users', uid, u);
    bump('users');
  }

  function backupTo(file) {
    fs.mkdirSync(path.dirname(path.resolve(file)), { recursive: true });
    if (fs.existsSync(file)) fs.unlinkSync(file);
    db.exec(`VACUUM INTO '${file.replace(/'/g, "''")}'`);
    return file;
  }

  // ---- whole-database JSON (Firebase-style tree) : used for the .json backup and for restore ----
  function exportAll() {
    const tree = {};
    for (const r of db.prepare('SELECT col, id, json FROM docs').all()) {
      const v = JSON.parse(r.json);
      if (r.col === ERR_COL) { tree.analytics = tree.analytics || {}; (tree.analytics.errors = tree.analytics.errors || {})[r.id] = v; }
      else (tree[r.col] = tree[r.col] || {})[r.id] = v;
    }
    return tree;
  }
  function importTree(data) {
    let n = 0;
    transaction(() => {
      for (const [col, val] of Object.entries(data || {})) {
        if (col === 'online_users') continue;
        if (col === 'analytics') {
          const errs = clean(val && val.errors);
          if (errs) { writeAt(['analytics', 'errors'], errs); n += Object.keys(errs).length; }
          continue;
        }
        const v = clean(val);
        if (v === undefined) continue;
        writeAt([col], v);
        n += (v && typeof v === 'object') ? Object.keys(v).length : 1;
      }
    });
    return n;
  }
  const isEmpty = () => one('SELECT COUNT(*) FROM docs') === 0;

  // ---- replication (main -> workers) ----
  function changesSince(since, limit = 1000) {
    if (since > curSeq) return { reset: true, epoch: getMeta('epoch'), seq: curSeq };
    const docs = db.prepare('SELECT col, id, json, seq FROM docs WHERE seq > ? ORDER BY seq LIMIT ?').all(since, limit);
    const dels = db.prepare('SELECT col, id, seq FROM tomb WHERE seq > ? ORDER BY seq LIMIT ?').all(since, limit);
    let upto = curSeq;
    if (docs.length === limit) upto = Math.min(upto, Number(docs[docs.length - 1].seq));
    if (dels.length === limit) upto = Math.min(upto, Number(dels[dels.length - 1].seq));
    const more = upto < curSeq;
    return {
      epoch: getMeta('epoch'), seq: upto, more,
      docs: docs.filter(d => d.seq <= upto).map(d => ({ col: d.col, id: d.id, json: d.json, seq: Number(d.seq) })),
      dels: dels.filter(d => d.seq <= upto).map(d => ({ col: d.col, id: d.id, seq: Number(d.seq) })),
      colver: more ? null : Object.fromEntries(colSeq),
    };
  }
  function applyChanges(p) {
    transaction(() => {
      const items = [...p.docs.map(d => ({ t: 'd', ...d })), ...p.dels.map(d => ({ t: 'x', ...d }))].sort((a, b) => a.seq - b.seq);
      for (const it of items) {
        if (it.t === 'd') q.put.run(it.col, it.id, it.json, Date.now(), it.seq); else q.del.run(it.col, it.id);
        if (it.col === 'settings') adminCache = null;
      }
      if (p.colver) for (const [c, s] of Object.entries(p.colver)) { colSeq.set(c, s); q.setVer.run(c, s); }
      curSeq = Math.max(curSeq, p.seq);
      setMeta('sync_seq', p.seq);
      if (p.epoch) setMeta('epoch_main', p.epoch);
    });
  }
  function wipeReplica() {
    transaction(() => { db.exec('DELETE FROM docs; DELETE FROM tomb; DELETE FROM colver;'); db.exec("DELETE FROM meta WHERE k IN ('sync_seq','epoch_main')"); });
    colSeq.clear(); curSeq = 0; adminCache = null;
  }

  // ---- worker registry (main only) ----
  const sha = (s) => crypto.createHash('sha256').update(s).digest('hex');
  function createWorker(name, url) {
    const id = crypto.randomBytes(4).toString('hex');
    const key = 'wk_' + crypto.randomBytes(24).toString('base64url');
    db.prepare('INSERT INTO workers (id, name, key_hash, url, created) VALUES (?, ?, ?, ?, ?)').run(id, String(name).slice(0, 60), sha(key), url || null, Date.now());
    return { id, name, key };
  }
  const listWorkers = () => db.prepare('SELECT id, name, url, created, last_seen, last_seq FROM workers ORDER BY created').all()
    .map(w => ({ ...w, created: Number(w.created), last_seen: Number(w.last_seen), last_seq: Number(w.last_seq) }));
  const deleteWorker = (id) => db.prepare('DELETE FROM workers WHERE id = ?').run(id).changes > 0;
  const workerByKey = (key) => (key && key.startsWith('wk_')) ? (db.prepare('SELECT id, name, url FROM workers WHERE key_hash = ?').get(sha(key)) || null) : null;
  const touchWorker = (id, url, seq) => db.prepare('UPDATE workers SET last_seen = ?, last_seq = ?, url = COALESCE(?, url) WHERE id = ?').run(Date.now(), seq, url || null, id);

  return {
    db, readRaw, writeAt, transaction, query, isAdmin, touchLastSeen, backupTo,
    exportAll, importTree, isEmpty, getMeta, setMeta,
    changesSince, applyChanges, wipeReplica, seq: () => curSeq,
    setOnChange: (fn) => { onChange = fn; },
    workerTargets: () => db.prepare("SELECT id, url, key_hash FROM workers WHERE url IS NOT NULL AND url != ''").all(),
    createWorker, listWorkers, deleteWorker, workerByKey, touchWorker,
    version: (segs) => segs[0] === 'online_users' ? (ver.get('online_users') || 0) : (colSeq.get(colInfo(segs).col) || 0),
    close: () => db.close(),
  };
}
