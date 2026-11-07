// Port of database.rules.json (the old Firebase Realtime Database rules) to plain JS.
// canRead() / checkWrite() are the only places that decide who may touch which path.
import { setIn, getIn } from './store.js';
import { deny } from './util.js';

const isNum = (x) => typeof x === 'number';
const isObj = (x) => x && typeof x === 'object';

const PUBLIC_READ = new Set(['settings']);
const AUTH_READ = new Set(['movies', 'banners', 'categories', 'socialLinks', 'notifications', 'stats', 'searchStats']);
const ADMIN_WRITE = new Set(['settings', 'movies', 'banners', 'categories', 'socialLinks', 'notifications']);

export function canRead(segs, auth, isAdmin) {
  const c = segs[0];
  if (PUBLIC_READ.has(c)) return true;
  if (AUTH_READ.has(c)) return !!auth;
  switch (c) {
    case 'users': return isAdmin || (segs.length >= 2 && !!auth && auth.uid === segs[1]);
    case 'online_users': case 'broken_links': return isAdmin;
    case 'analytics': return segs[1] === 'errors' && isAdmin;
    case 'pwd_resets': return isAdmin || segs.length >= 2;   // logged-out "forgot password" screen needs its own key
    default: return false;
  }
}

// newVal: already cleaned value that will be stored at `segs` (undefined = delete).
export function checkWrite(segs, newVal, { auth, isAdmin, store }) {
  const c = segs[0];

  if (ADMIN_WRITE.has(c)) { if (!isAdmin) deny(); return; }

  switch (c) {
    case 'users': {
      if (segs.length < 2) deny();
      const uid = segs[1];
      const self = !!auth && auth.uid === uid;
      if (!isAdmin && !self) deny();
      const oldDoc = store.readRaw(['users', uid]);
      const newDoc = setIn(oldDoc, segs.slice(2), newVal);
      if (!isAdmin) {
        if (newDoc === undefined && !(oldDoc && oldDoc.status === 'deleted')) deny();
        if (newDoc) {   // validation rules are not evaluated on deletes (same as Firebase)
          const oldS = oldDoc ? oldDoc.status : undefined;
          if (newDoc.status !== oldS && !(oldS === undefined && newDoc.status === 'active')) deny();
        }
      }
      if (newDoc) {
        for (const v of Object.values(newDoc.myList || {})) if (!(isNum(v) || v === true)) deny();
        for (const v of Object.values(newDoc.progress || {})) if (!(isObj(v) && v.t !== undefined && v.d !== undefined)) deny();
      }
      return;
    }

    case 'online_users': {
      if (segs.length < 2) deny();
      if (!isAdmin && !(auth && auth.uid === segs[1])) deny();
      return;
    }

    case 'broken_links': {
      if (segs.length < 2) deny();
      if (!isAdmin && !(auth && newVal !== undefined)) deny();
      return;
    }

    case 'analytics': {
      if (segs[1] === 'errors' && segs.length === 3) {
        if (!auth || newVal === undefined) deny();
        if (store.readRaw(segs) !== undefined) deny();           // write-once
        if (JSON.stringify(newVal).length > 4000) deny();
        return;
      }
      if (isAdmin && newVal === undefined) return;               // admin clean-up of legacy nodes
      deny();
      return;
    }

    case 'pwd_resets': {   // logged-out users write their own reset timestamps (as in the original design)
      if (segs.length < 2) deny();
      const doc = setIn(store.readRaw(['pwd_resets', segs[1]]), segs.slice(2), newVal);
      for (const v of Object.values(doc || {})) if (!isNum(v)) deny();
      return;
    }

    case 'stats': {
      const checkDoc = (d) => { for (const [k, v] of Object.entries(d || {})) if (!(k === 'plays' || k === 'lists') || !isNum(v)) deny(); };
      if (segs.length === 1) {
        if (!isAdmin) deny();
        for (const d of Object.values(newVal || {})) checkDoc(d);
        return;
      }
      const oldDoc = store.readRaw(['stats', segs[1]]);
      const newDoc = setIn(oldDoc, segs.slice(2), newVal);
      if (!isAdmin) {
        if (!auth || segs.length !== 3 || !(segs[2] === 'plays' || segs[2] === 'lists')) deny();
        const old = (oldDoc && oldDoc[segs[2]]) || 0;
        if (!isNum(newVal)) deny();
        if (segs[2] === 'plays' && newVal !== old + 1) deny();
        if (segs[2] === 'lists' && !(newVal >= 0 && (newVal === old + 1 || newVal === old - 1))) deny();
      }
      checkDoc(newDoc);
      return;
    }

    case 'searchStats': {
      if (segs.length === 1) {
        if (!isAdmin) deny();
        for (const d of Object.values(newVal || {})) validSearch(d, undefined);
        return;
      }
      if (!auth) deny();
      const oldDoc = store.readRaw(['searchStats', segs[1]]);
      const newDoc = setIn(oldDoc, segs.slice(2), newVal);
      if (newDoc === undefined) { if (!isAdmin) deny(); return; }
      validSearch(newDoc, oldDoc);
      return;
    }

    default:
      // Unknown path: nobody may write, except an admin deleting leftovers of removed features.
      if (isAdmin && newVal === undefined) return;
      deny();
  }
}

function validSearch(d, old) {
  if (!isObj(d)) deny();
  for (const k of Object.keys(d)) if (!['t', 'c', 'at'].includes(k)) deny();
  if (typeof d.t !== 'string' || d.t.length < 2 || d.t.length > 40) deny();
  if (!isNum(d.at)) deny();
  if (!isNum(d.c) || d.c !== ((old && old.c) || 0) + 1) deny();
}

export { getIn };
