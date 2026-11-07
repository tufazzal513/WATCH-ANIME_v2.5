// On start-up of a MAIN server with an empty database: pull the newest backup from the Telegram channel.
// Order: .db file first (exact copy), then the .json file as a fallback.
import fs from 'node:fs';
import crypto from 'node:crypto';
import { DatabaseSync } from 'node:sqlite';
import { openStore } from './store.js';

export async function restoreIfEmpty({ dbPath, tg, log = console }) {
  if (!tg || !tg.enabled) return { restored: false, reason: 'telegram_not_configured' };
  let empty = true;
  if (fs.existsSync(dbPath) && fs.statSync(dbPath).size > 0) { const s = openStore(dbPath); empty = s.isEmpty(); s.close(); }
  if (!empty) return { restored: false, reason: 'database_not_empty' };

  let l;
  try { l = await tg.latest(); } catch (e) { log.error('[restore] cannot reach Telegram:', e.message); return { restored: false, reason: 'telegram_error', error: e.message }; }
  if (!l) { log.log('[restore] no pinned backup in the channel - starting with an empty database'); return { restored: false, reason: 'no_backup' }; }

  if (l.db) {
    try {
      const buf = await tg.download(l.db);
      const tmp = dbPath + '.restore';
      fs.mkdirSync(require_dir(dbPath), { recursive: true });
      fs.writeFileSync(tmp, buf);
      const t = new DatabaseSync(tmp); const n = Number(t.prepare('SELECT COUNT(*) AS c FROM docs').get().c); t.close();
      if (!n) throw new Error('backup_db_is_empty');
      for (const ext of ['', '-wal', '-shm']) fs.rmSync(dbPath + ext, { force: true });
      fs.renameSync(tmp, dbPath);
      const s = openStore(dbPath); s.setMeta('epoch', crypto.randomBytes(6).toString('hex')); s.close();   // new epoch -> workers resync
      log.log(`[restore] restored ${n} documents from the Telegram .db backup (${new Date(l.date * 1000).toISOString()})`);
      return { restored: true, from: 'db', docs: n };
    } catch (e) { log.error('[restore] .db restore failed, trying .json:', e.message); }
  }
  try {
    const buf = await tg.download(l.json);
    const s = openStore(dbPath);
    const n = s.importTree(JSON.parse(buf.toString('utf8')));
    s.setMeta('epoch', crypto.randomBytes(6).toString('hex')); s.close();
    log.log(`[restore] restored ${n} items from the Telegram .json backup`);
    return { restored: true, from: 'json', docs: n };
  } catch (e) { log.error('[restore] .json restore failed:', e.message); return { restored: false, reason: 'restore_failed', error: e.message }; }
}
function require_dir(p) { return p.replace(/[\\/][^\\/]*$/, '') || '.'; }
