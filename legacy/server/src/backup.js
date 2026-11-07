// Backups: VACUUM INTO (consistent copy while the server keeps running) -> gzip -> keep N locally
// -> optionally upload to Google Drive (OAuth refresh token, uses the owner's own Drive quota).
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { pipeline } from 'node:stream/promises';

const stamp = () => new Date().toISOString().replace(/[-:]/g, '').replace(/\..+/, '').replace('T', '-');

async function driveToken(cfg) {
  const r = await fetch('https://oauth2.googleapis.com/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ client_id: cfg.clientId, client_secret: cfg.clientSecret, refresh_token: cfg.refreshToken, grant_type: 'refresh_token' }),
  });
  if (!r.ok) throw new Error('drive_token_failed ' + r.status);
  return (await r.json()).access_token;
}

async function driveUpload(cfg, token, file, name) {
  const meta = JSON.stringify({ name, parents: cfg.folderId ? [cfg.folderId] : undefined });
  const boundary = 'mx' + Date.now();
  const body = Buffer.concat([
    Buffer.from(`--${boundary}\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n${meta}\r\n--${boundary}\r\nContent-Type: application/gzip\r\n\r\n`),
    fs.readFileSync(file),
    Buffer.from(`\r\n--${boundary}--`),
  ]);
  const r = await fetch('https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,name', {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + token, 'Content-Type': `multipart/related; boundary=${boundary}` },
    body,
  });
  if (!r.ok) throw new Error('drive_upload_failed ' + r.status + ' ' + (await r.text()).slice(0, 200));
  return r.json();
}

async function driveRotate(cfg, token, keepDays) {
  const parent = cfg.folderId ? `'${cfg.folderId}' in parents and ` : '';
  const url = 'https://www.googleapis.com/drive/v3/files?pageSize=1000&fields=files(id,name,createdTime)&q=' +
    encodeURIComponent(`${parent}trashed=false and name contains 'movex-'`);
  const r = await fetch(url, { headers: { Authorization: 'Bearer ' + token } });
  if (!r.ok) return;
  const cutoff = Date.now() - keepDays * 86400e3;
  for (const f of (await r.json()).files || []) {
    if (/^movex-.*\.db\.gz$/.test(f.name) && Date.parse(f.createdTime) < cutoff) {
      await fetch('https://www.googleapis.com/drive/v3/files/' + f.id, { method: 'DELETE', headers: { Authorization: 'Bearer ' + token } });
    }
  }
}

export function createBackupManager({ store, dir, keepLocal = 7, drive = null, driveKeepDays = 30, tg = null, log = console }) {
  const val = (x) => (typeof x === 'function' ? x() : x);   // settings can change at any time from the admin panel
  fs.mkdirSync(dir, { recursive: true });
  const metaFile = path.join(dir, 'last-backup.json');
  let running = false;

  async function run() {
    if (running) return { skipped: true };
    running = true;
    try {
      const name = `movex-${stamp()}.db.gz`;
      const tmp = path.join(dir, `.tmp-${Date.now()}.db`);
      store.backupTo(tmp);
      const out = path.join(dir, name);
      await pipeline(fs.createReadStream(tmp), zlib.createGzip({ level: 9 }), fs.createWriteStream(out));
      const tmpKeep = tmp;

      const files = fs.readdirSync(dir).filter(f => /^movex-.*\.db\.gz$/.test(f)).sort();
      for (const f of files.slice(0, Math.max(0, files.length - Number(val(keepLocal) || 7)))) fs.unlinkSync(path.join(dir, f));

      const result = { file: out, size: fs.statSync(out).size, drive: null, telegram: null };
      if (tg && tg.enabled) {
        try {
          const dbBuf = fs.readFileSync(tmpKeep);
          const jsonBuf = Buffer.from(JSON.stringify(store.exportAll()));
          result.telegram = await tg.upload({ dbBuf, jsonBuf, stamp: stamp(), info: `db ${(dbBuf.length / 1048576).toFixed(1)} MB, json ${(jsonBuf.length / 1048576).toFixed(1)} MB` });
        } catch (e) { result.telegramError = String(e.message || e); log.error('[backup] Telegram upload failed:', result.telegramError); }
      }
      fs.rmSync(tmpKeep, { force: true });
      const dv = val(drive);
      if (dv && dv.clientId && dv.clientSecret && dv.refreshToken) {
        try {
          const token = await driveToken(dv);
          const up = await driveUpload(dv, token, out, name);
          await driveRotate(dv, token, Number(val(driveKeepDays) || 30));
          result.drive = up.id;
        } catch (e) { result.driveError = String(e.message || e); log.error('[backup] Google Drive upload failed:', result.driveError); }
      }
      fs.writeFileSync(metaFile, JSON.stringify({ at: Date.now(), ...result }));
      log.log('[backup] done', result);
      return result;
    } finally { running = false; }
  }

  const readMeta = () => { try { return JSON.parse(fs.readFileSync(metaFile, 'utf8')); } catch { return {}; } };
  const lastAt = () => readMeta().at || 0;
  const status = () => { const m = readMeta(); return { at: m.at || 0, size: m.size || 0, telegram: !!m.telegram, telegramError: m.telegramError || null, drive: !!m.drive, driveError: m.driveError || null, running, telegramConfigured: !!(tg && tg.enabled), telegramMode: (tg && tg.mode) || null }; };

  function schedule(everyHours) {
    const tick = () => { if (Date.now() - lastAt() >= Number(val(everyHours) || 12) * 3600e3) run().catch(e => log.error('[backup] failed', e)); };
    setTimeout(tick, 30e3).unref();
    return setInterval(tick, 600e3).unref();
  }
  return { run, schedule, lastAt, status };
}
