// Usage: node scripts/import-firebase-json.js <firebase-export.json>
// Firebase console -> Realtime Database -> (three dots) -> Export JSON, then run this once.
import fs from 'node:fs';
import { openStore, clean, ERR_COL } from '../src/store.js';

const file = process.argv[2];
if (!file) { console.error('Usage: node scripts/import-firebase-json.js <firebase-export.json>'); process.exit(1); }
const data = JSON.parse(fs.readFileSync(file, 'utf8')) || {};
const store = openStore(process.env.DB_PATH || './data/database.db');
const skip = new Set(['online_users']);   // presence is live-only
let n = 0;
store.transaction(() => {
  for (const [col, val] of Object.entries(data)) {
    if (skip.has(col)) continue;
    if (col === 'analytics') {
      const errs = clean(val && val.errors);
      if (errs) { store.writeAt(['analytics', 'errors'], errs); n += Object.keys(errs).length; }
      continue;
    }
    const v = clean(val);
    if (v === undefined) continue;
    store.writeAt([col], v);
    n += (v && typeof v === 'object') ? Object.keys(v).length : 1;
    console.log('imported', col, '->', (v && typeof v === 'object') ? Object.keys(v).length + ' items' : 'value');
  }
});
console.log('done,', n, 'top-level items. Admins imported from settings/adminUIDs:', Object.keys((data.settings && data.settings.adminUIDs) || {}).length);
store.close();
