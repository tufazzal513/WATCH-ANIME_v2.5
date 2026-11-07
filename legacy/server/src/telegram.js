// Telegram channel as an off-site backup vault.
//   upload(): sends  movex-<stamp>.db  then  movex-<stamp>.json (as a reply to the .db message) and PINS the .json
//   message. The pinned message is the "latest backup" marker: on a brand-new server latest() finds it and
//   download() pulls the files back.
//
// Two transports (picked automatically):
//   MTProto  (TG_API_ID + TG_API_HASH + TG_SESSION or TG_BOT_TOKEN)  -> files up to ~2 GB both ways. Needs `npm install` (package "telegram" / GramJS).
//   Bot API  (TG_BOT_TOKEN only)                                     -> no packages, but bots can only DOWNLOAD 20 MB (upload 50 MB).
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

const API = 'https://api.telegram.org';
const BOT_UP = 49 * 1024 * 1024;
const MT_UP = 1900 * 1024 * 1024;
export const MAX_DOWN = 20 * 1024 * 1024;
const gunzipIf = (buf) => (buf[0] === 0x1f && buf[1] === 0x8b) ? zlib.gunzipSync(buf) : buf;

// ---------------------------------------------------------------- Bot API
function botTransport({ token, chatId }) {
  async function call(method, body) {
    const r = await fetch(`${API}/bot${token}/${method}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) });
    const j = await r.json().catch(() => ({}));
    if (!j.ok) throw new Error(`telegram_${method}_failed: ${j.description || r.status}`);
    return j.result;
  }
  return {
    mode: 'bot-api', limit: BOT_UP, call,
    async send(buf, filename, caption, replyTo) {
      const fd = new FormData();
      fd.set('chat_id', String(chatId)); fd.set('caption', caption); fd.set('disable_notification', 'true');
      if (replyTo) fd.set('reply_parameters', JSON.stringify({ message_id: replyTo }));
      fd.set('document', new Blob([buf]), filename);
      const r = await fetch(`${API}/bot${token}/sendDocument`, { method: 'POST', body: fd });
      const j = await r.json().catch(() => ({}));
      if (!j.ok) throw new Error(`telegram_sendDocument_failed: ${j.description || r.status}`);
      return j.result.message_id;
    },
    pin: (id) => call('pinChatMessage', { chat_id: chatId, message_id: id, disable_notification: true }),
    unpin: (id) => call('unpinChatMessage', { chat_id: chatId, message_id: id }),
    del: (id) => call('deleteMessage', { chat_id: chatId, message_id: id }),
    async latest() {
      const chat = await call('getChat', { chat_id: chatId });
      const pm = chat.pinned_message;
      if (!pm || !pm.document) return null;
      return { json: pm.document, db: (pm.reply_to_message && pm.reply_to_message.document) || null, date: pm.date };
    },
    async download(doc) {
      if (doc.file_size && doc.file_size > MAX_DOWN) throw new Error('file_over_20MB: Bot API cannot download it. Set TG_API_ID, TG_API_HASH (MTProto) to lift the limit');
      const f = await call('getFile', { file_id: doc.file_id });
      const r = await fetch(`${API}/file/bot${token}/${f.file_path}`);
      if (!r.ok) throw new Error('telegram_download_failed ' + r.status);
      return gunzipIf(Buffer.from(await r.arrayBuffer()));
    },
  };
}

// ---------------------------------------------------------------- MTProto (GramJS)
function mtprotoTransport({ apiId, apiHash, session, token, chatId, dir, log }) {
  let lib = null, client = null, entity = null, connecting = null;
  const sessFile = path.join(dir, 'tg-bot.session');

  async function load() {
    if (lib) return lib;
    let tl, ss, up;
    try { tl = await import('telegram'); ss = await import('telegram/sessions/index.js'); up = await import('telegram/client/uploads.js'); }
    catch { throw new Error('gramjs_not_installed: run "npm install" in the server folder (package "telegram")'); }
    const pick = (m, k) => m[k] || (m.default && m.default[k]);
    lib = { TelegramClient: pick(tl, 'TelegramClient'), Api: pick(tl, 'Api'), StringSession: pick(ss, 'StringSession'), CustomFile: pick(up, 'CustomFile') };
    return lib;
  }
  async function connect() {
    if (client) return client;
    if (connecting) return connecting;
    connecting = (async () => {
      const { TelegramClient, StringSession } = await load();
      let saved = session || '';
      if (!saved && token) { try { saved = fs.readFileSync(sessFile, 'utf8').trim(); } catch { /* first run */ } }
      const c = new TelegramClient(new StringSession(saved), Number(apiId), String(apiHash), { connectionRetries: 5 });
      if (session) {                                  // a logged-in user session (scripts/tg-login.js)
        await c.connect();
        if (!(await c.checkAuthorization())) throw new Error('telegram_session_invalid: create a new TG_SESSION with scripts/tg-login.js');
      } else {                                        // bot token over MTProto
        await c.start({ botAuthToken: token });
        try { fs.mkdirSync(dir, { recursive: true }); fs.writeFileSync(sessFile, c.session.save()); } catch { /* ignore */ }
      }
      const id = String(chatId);
      const asKey = /^-?\d+$/.test(id) ? BigInt(id) : id;
      try { entity = await c.getEntity(asKey); }
      catch { try { await c.getDialogs({ limit: 200 }); entity = await c.getEntity(asKey); } catch (e) { throw new Error('telegram_channel_not_found: ' + (e.message || e) + ' (bots: use the channel @username as TG_CHAT_ID, or use a user TG_SESSION)'); } }
      client = c; return c;
    })();
    try { return await connecting; } finally { connecting = null; }
  }

  return {
    mode: 'mtproto', limit: MT_UP,
    async close() { try { if (client) await client.disconnect(); } catch { /* ignore */ } client = null; },
    async send(buf, filename, caption, replyTo) {
      const c = await connect(); const { CustomFile } = await load();
      const m = await c.sendFile(entity, { file: new CustomFile(filename, buf.length, '', buf), caption, forceDocument: true, silent: true, workers: 4, ...(replyTo ? { replyTo } : {}) });
      return m.id;
    },
    async pin(id) { const c = await connect(); await c.pinMessage(entity, id, { notify: false }); },
    async unpin(id) { const c = await connect(); await c.unpinMessage(entity, id); },
    async del(id) { const c = await connect(); await c.deleteMessages(entity, [id], { revoke: true }); },
    async latest() {
      const c = await connect(); const { Api } = await load();
      const pinned = await c.getMessages(entity, { filter: new Api.InputMessagesFilterPinned(), limit: 1 });   // newest pinned first
      const pm = pinned && pinned[0];
      if (!pm || !pm.media) return null;
      let db = null;
      const rid = pm.replyTo && (pm.replyTo.replyToMsgId || pm.replyToMsgId);
      if (rid) { const r = await c.getMessages(entity, { ids: [rid] }); if (r && r[0] && r[0].media) db = r[0]; }
      return { json: pm, db, date: pm.date };
    },
    async download(msg) {
      const c = await connect();
      const buf = await c.downloadMedia(msg, {});
      if (!buf || !buf.length) throw new Error('telegram_download_empty');
      return gunzipIf(Buffer.from(buf));
    },
  };
}

// ---------------------------------------------------------------- public API
export function createTelegram({ token, chatId, apiId, apiHash, session, keep = 30, dir = './data/backups', log = console }) {
  const useMt = !!(apiId && apiHash && (session || token));
  const enabled = !!(chatId && (useMt || token));
  const tr = !enabled ? null : (useMt ? mtprotoTransport({ apiId, apiHash, session, token, chatId, dir, log }) : botTransport({ token, chatId }));
  const histFile = path.join(dir, 'tg-history.json');
  const readHist = () => { try { return JSON.parse(fs.readFileSync(histFile, 'utf8')); } catch { return []; } };
  const fit = (buf, name) => buf.length > tr.limit ? { buf: zlib.gzipSync(buf), name: name + '.gz' } : { buf, name };

  async function upload({ dbBuf, jsonBuf, stamp, info }) {
    const a = fit(dbBuf, `movex-${stamp}.db`);
    const b = fit(jsonBuf, `movex-${stamp}.json`);
    const id1 = await tr.send(a.buf, a.name, `#movex #db ${stamp}\n${info || ''}`.trim());
    const id2 = await tr.send(b.buf, b.name, `#movex #json #LATEST ${stamp}\n${info || ''}`.trim(), id1);
    const hist = readHist();
    await tr.pin(id2);
    const prev = hist[hist.length - 1];
    if (prev) tr.unpin(prev[1]).catch(() => {});
    hist.push([id1, id2, Date.now()]);
    while (hist.length > keep) {                      // keep the channel tidy: drop the oldest backups
      const old = hist.shift();
      tr.del(old[0]).catch(() => {}); tr.del(old[1]).catch(() => {});
    }
    try { fs.mkdirSync(dir, { recursive: true }); fs.writeFileSync(histFile, JSON.stringify(hist)); } catch { /* ignore */ }
    return { db: id1, json: id2, dbBytes: a.buf.length, jsonBytes: b.buf.length, mode: tr.mode };
  }
  return { enabled, mode: tr ? tr.mode : null, close: async () => { if (tr && tr.close) await tr.close(); }, upload, latest: () => tr.latest(), download: (d) => tr.download(d) };
}
