// WORKER side: keeps a local SQLite replica of the main server's data by pulling changes
// (GET {MAIN}/api/sync?since=N).  Reads are then served from the local copy, writes are forwarded to main.
export function createPuller({ store, mainUrl, key, publicUrl, intervalMs = 5000, log = console }) {
  const base = mainUrl.replace(/\/+$/, '');
  let ready = store.getMeta('sync_seq') != null;   // a replica from an earlier run can serve right away
  let lastOk = 0, lastError = null, running = null, timer = null;

  async function pullAll() {
    for (let guard = 0; guard < 10000; guard++) {
      const since = Number(store.getMeta('sync_seq') || 0);
      const r = await fetch(`${base}/api/sync?since=${since}&limit=1000`, {
        headers: { 'X-Worker-Key': key, ...(publicUrl ? { 'X-Worker-Url': publicUrl } : {}) },
      });
      if (r.status === 401 || r.status === 403) throw new Error('worker_key_rejected (create/check the key in the admin panel)');
      if (!r.ok) throw new Error('sync_http_' + r.status);
      const p = await r.json();
      const known = store.getMeta('epoch_main');
      if (p.reset || (known && p.epoch && known !== p.epoch)) {   // main was restored/replaced -> start over
        log.log('[worker] main data set changed, doing a full resync');
        store.wipeReplica(); ready = false;
        if (p.epoch) store.setMeta('epoch_main', p.epoch);
        continue;
      }
      store.applyChanges(p);
      if (!p.more) { ready = true; lastOk = Date.now(); lastError = null; return; }
    }
  }
  const syncNow = () => {
    if (!running) running = pullAll().catch((e) => { lastError = String(e.message || e); log.error('[worker] sync failed:', lastError); }).finally(() => { running = null; });
    return running;
  };
  const start = () => { syncNow(); timer = setInterval(syncNow, intervalMs); timer.unref?.(); };
  return { start, syncNow, stop: () => clearInterval(timer), status: () => ({ ready, lastOk, lastError, seq: store.seq(), lagMs: lastOk ? Date.now() - lastOk : null }), get ready() { return ready; } };
}
