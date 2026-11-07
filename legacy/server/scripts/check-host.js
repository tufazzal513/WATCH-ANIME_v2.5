// Run this ONCE on the hosting (Botkeep) before deploying:   node scripts/check-host.js
// It checks the four things MoveX needs. Run it, restart the server, run it again: "disk survives restart" shows the result.
import fs from 'node:fs';
import path from 'node:path';

const ok = (m) => console.log('  OK   ' + m);
const bad = (m) => console.log('  FAIL ' + m);
const dir = process.env.CHECK_DIR || './data';
fs.mkdirSync(dir, { recursive: true });

console.log('Node version:', process.version);
const [maj, min] = process.versions.node.split('.').map(Number);
if (maj > 22 || (maj === 22 && min >= 13)) ok('Node >= 22.13 (built-in SQLite works)'); else bad('Node is older than 22.13 -> node:sqlite is unavailable; choose Node 22/24 in the panel');

try {
  const { DatabaseSync } = await import('node:sqlite');
  const db = new DatabaseSync(path.join(dir, 'check.db'));
  db.exec('CREATE TABLE IF NOT EXISTS t(x)');
  db.prepare('INSERT INTO t VALUES (?)').run(Date.now());
  const n = db.prepare('SELECT COUNT(*) AS c FROM t').get().c;
  db.close();
  ok('SQLite works, rows in check.db: ' + n + (n > 1 ? '   <-- disk survives restart' : '   (restart the server and run again: it must show 2)'));
} catch (e) { bad('SQLite: ' + e.message); }

for (const [name, url] of [
  ['Google token keys (needed to verify logins)', 'https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com'],
  ['Google Drive/OAuth (only for Drive backups)', 'https://oauth2.googleapis.com/token'],
]) {
  try {
    const r = await fetch(url, { method: name.includes('OAuth') ? 'POST' : 'GET', signal: AbortSignal.timeout(8000) });
    ok(name + ' reachable (HTTP ' + r.status + ')');
  } catch (e) { bad(name + ' NOT reachable: ' + (e.cause?.code || e.message)); }
}
const m = process.memoryUsage();
console.log('Memory now: rss ' + Math.round(m.rss / 1048576) + ' MB (limit on the free plan: 512 MB)');
