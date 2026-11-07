// Usage: node scripts/add-admin.js <firebase-uid>   (adds the UID to settings/adminUIDs)
import { openStore } from '../src/store.js';
const uid = process.argv[2];
if (!uid || /[.$#\[\]\/]/.test(uid)) { console.error('Usage: node scripts/add-admin.js <firebase-uid>'); process.exit(1); }
const store = openStore(process.env.DB_PATH || './data/database.db');
store.writeAt(['settings', 'adminUIDs', uid], true);
console.log('admin added:', uid);
store.close();
