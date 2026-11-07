// Settings that used to be environment variables, editable from the admin panel (MAIN server only).
// Saved in the database's `meta` table: they are included in every backup, are NOT replicated to workers,
// and a saved value overrides the environment variable. Secrets are write-only: the API never returns them.
const ORIGINS = (v) => v.split(',').map(s => s.trim().replace(/\/+$/, '')).filter(Boolean);
const num = (min, max) => (v) => { const n = Number(v); return Number.isFinite(n) && n >= min && n <= max ? String(Math.round(n)) : null; };
const re = (rx, norm = (x) => x) => (v) => rx.test(v) ? norm(v) : null;

export const FIELDS = [
  { key: 'ALLOWED_ORIGINS', group: 'General', label: 'Allowed website addresses', help: 'Your site address(es), comma separated, e.g. https://you.github.io (no path). "*" = any site. Locked out by a mistake? Set RESET_PANEL_SETTINGS=1 on the host once.', def: '*',
    check: (v) => { const l = ORIGINS(v); return l.length && l.every(o => o === '*' || /^https?:\/\/[^\s/]+$/.test(o)) ? l.join(',') : null; } },
  { key: 'TRUST_PROXY', group: 'General', label: 'Server is behind a proxy', type: 'bool', help: 'Turn on when the host puts a proxy in front (so rate limits see the real visitor IP). Usually ON.', def: '0', check: re(/^[01]$/) },

  { key: 'BACKUP_EVERY_HOURS', group: 'Backup', label: 'Auto backup every (hours)', type: 'number', def: '12', check: num(1, 720) },
  { key: 'BACKUP_KEEP_LOCAL', group: 'Backup', label: 'Local backups to keep', type: 'number', def: '7', check: num(1, 60) },

  { key: 'TG_BOT_TOKEN', group: 'Telegram', label: 'Bot token', secret: true, help: 'From @BotFather. Add the bot as ADMIN (post + pin) of your channel.', check: re(/^\d{5,}:[\w-]{20,}$/) },
  { key: 'TG_CHAT_ID', group: 'Telegram', label: 'Channel ID or @username', help: '-100xxxxxxxxxx for a private channel, or @name for a public one.', check: re(/^(-?\d{5,}|@\w{4,})$/) },
  { key: 'TG_API_ID', group: 'Telegram', label: 'API ID (MTProto, big files)', help: 'my.telegram.org -> API development tools. Needs "npm install" once on the server.', check: re(/^\d{3,12}$/) },
  { key: 'TG_API_HASH', group: 'Telegram', label: 'API hash (MTProto)', secret: true, check: re(/^[0-9a-fA-F]{32}$/) },
  { key: 'TG_SESSION', group: 'Telegram', label: 'Login session (MTProto)', secret: true, help: 'Optional. Made with "npm run tg-login". Lets a user account access a private channel.', check: re(/^[\w+/=.-]{20,10000}$/) },
  { key: 'TG_KEEP', group: 'Telegram', label: 'Backups kept in the channel', type: 'number', def: '30', check: num(2, 500) },

  { key: 'GDRIVE_CLIENT_ID', group: 'Google Drive', label: 'Client ID', check: re(/^[^\s]{5,300}$/) },
  { key: 'GDRIVE_CLIENT_SECRET', group: 'Google Drive', label: 'Client secret', secret: true, check: re(/^[^\s]{5,300}$/) },
  { key: 'GDRIVE_REFRESH_TOKEN', group: 'Google Drive', label: 'Refresh token', secret: true, check: re(/^[^\s]{5,600}$/) },
  { key: 'GDRIVE_FOLDER_ID', group: 'Google Drive', label: 'Folder ID (optional)', check: re(/^[\w-]{5,200}$/) },
  { key: 'GDRIVE_KEEP_DAYS', group: 'Google Drive', label: 'Keep Drive backups (days)', type: 'number', def: '30', check: num(1, 3650) },
];
const BY_KEY = new Map(FIELDS.map(f => [f.key, f]));

export function createConfig({ store, env = {} }) {
  const listeners = [];
  const load = () => { try { const j = JSON.parse(store.getMeta('config') || '{}'); return j && typeof j === 'object' ? j : {}; } catch { return {}; } };
  let saved = load();
  const str = (x) => (x === undefined || x === null) ? '' : String(x);

  const get = (k) => { const s = str(saved[k]); if (s !== '') return s; const e = str(env[k]); if (e !== '') return e; return (BY_KEY.get(k) || {}).def || ''; };
  const source = (k) => str(saved[k]) !== '' ? 'panel' : (str(env[k]) !== '' ? 'env' : 'default');

  // values: { KEY: "text" }  ->  "" or null clears the saved value (falls back to env/default). Secrets that are simply left out stay unchanged.
  function set(values) {
    const next = { ...saved }, changed = [], errors = {};
    for (const [k, raw] of Object.entries(values || {})) {
      const f = BY_KEY.get(k);
      if (!f) { errors[k] = 'unknown setting'; continue; }
      const v = raw === null ? '' : str(raw).trim();
      if (v === '') { if (next[k] !== undefined) { delete next[k]; changed.push(k); } continue; }
      const norm = f.check(v);
      if (norm === null) { errors[k] = 'invalid value'; continue; }
      if (next[k] !== norm) { next[k] = norm; changed.push(k); }
    }
    if (Object.keys(errors).length) return { ok: false, errors };
    if (changed.length) { saved = next; store.setMeta('config', JSON.stringify(saved)); listeners.forEach(fn => { try { fn(changed); } catch { /* ignore */ } }); }
    return { ok: true, changed };
  }

  // What the admin page may see. Secrets: only "is it set" + the last 4 characters.
  const view = () => FIELDS.map(f => {
    const cur = get(f.key), o = { key: f.key, group: f.group, label: f.label, type: f.type || 'text', secret: !!f.secret, help: f.help || '', source: source(f.key), set: cur !== '' };
    if (f.secret) o.hint = cur ? '••••' + cur.slice(-4) : ''; else o.value = cur;
    return o;
  });
  return { get, set, view, source, onChange: (fn) => listeners.push(fn) };
}
