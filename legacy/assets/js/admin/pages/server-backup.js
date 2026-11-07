// Admin -> Settings -> "Servers & Backup": manual backup, backup status, worker servers.
(function () {
  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const ago = (t) => { if (!t) return 'never'; const s = Math.max(0, (Date.now() - t) / 1000); return s < 90 ? Math.round(s) + 's ago' : s < 5400 ? Math.round(s / 60) + ' min ago' : s < 129600 ? Math.round(s / 3600) + ' h ago' : Math.round(s / 86400) + ' d ago'; };
  const mb = (n) => (n / 1048576).toFixed(1) + ' MB';
  let busy = false;

  function render(st) {
    const b = st.backup || {};
    $('srv-summary').textContent = 'This is the MAIN server (owns the database). Change counter: ' + st.seq + '. Workers: ' + (st.workers || []).length + '.';
    $('bk-status').innerHTML = !b.telegramConfigured
      ? '<span class="text-yellow-400">Telegram is not set up</span> (set TG_BOT_TOKEN and TG_CHAT_ID on the server). Local backups still run.'
      : 'Last backup: <b>' + esc(ago(b.at)) + '</b>' + (b.size ? ' (' + mb(b.size) + ' gz)' : '') + ' &middot; Telegram (' + esc(b.telegramMode || '') + '): ' + (b.telegram ? '<span class="text-emerald-400">uploaded</span>' : '<span class="text-red-400">' + esc(b.telegramError || 'not yet') + '</span>');
    $('wk-list').innerHTML = (st.workers || []).map((w) =>
      '<div class="flex items-center justify-between gap-2 bg-gray-700/60 rounded-lg px-3 py-2 text-sm">' +
        '<div class="min-w-0"><div class="font-bold text-white truncate"><span class="inline-block w-2 h-2 rounded-full mr-2 ' + (w.online ? 'bg-emerald-400' : 'bg-red-500') + '"></span>' + esc(w.name) + '</div>' +
        '<div class="text-[11px] text-gray-400 truncate">' + esc(w.url || 'no public URL yet') + ' &middot; seen ' + esc(ago(w.lastSeen)) + (w.behind > 0 ? ' &middot; ' + w.behind + ' changes behind' : '') + '</div></div>' +
        '<button data-del="' + esc(w.id) + '" class="text-red-400 hover:text-red-300 text-xs font-bold shrink-0">Remove</button></div>'
    ).join('') || '<p class="text-xs text-gray-500">No workers yet. Add one below, then deploy the same code on another server with ROLE=worker.</p>';
  }

  async function load() {
    if (busy) return;
    try { render(await mxApi('GET', '/api/admin/status')); }
    catch (e) { $('srv-summary').textContent = 'Could not load server status (' + (e.code || e.message) + ').'; }
  }

  document.addEventListener('click', async (ev) => {
    const t = ev.target.closest && ev.target.closest('button');
    if (!t) return;
    if (t.id === 'bk-run') {
      t.disabled = true; busy = true; const old = t.innerHTML; t.innerHTML = '<i class="fas fa-spinner fa-spin mr-2"></i>Backing up...';
      try { const r = await mxApi('POST', '/api/admin/backup', {}); alert(r.skipped ? 'A backup is already running.' : (r.telegram ? 'Backup saved and uploaded to Telegram.' : 'Local backup done. Telegram: ' + (r.telegramError || 'not configured'))); }
      catch (e) { alert('Backup failed: ' + (e.code || e.message)); }
      t.disabled = false; t.innerHTML = old; busy = false; load();
    } else if (t.id === 'bk-dl') {
      try { await window.mxBackupDownload(); } catch (e) { alert('Download failed: ' + e.message); }
    } else if (t.dataset && t.dataset.del) {
      if (!confirm('Remove this worker? It will stop syncing.')) return;
      try { await mxApi('DELETE', '/api/admin/workers?id=' + encodeURIComponent(t.dataset.del)); } catch (e) { alert('Failed: ' + (e.code || e.message)); }
      load();
    }
  });

  document.addEventListener('submit', async (ev) => {
    if (!ev.target || ev.target.id !== 'wk-form') return;
    ev.preventDefault();
    const name = $('wk-name').value.trim(), url = $('wk-url').value.trim();
    if (!name) return alert('Give the worker a name.');
    try {
      const w = await mxApi('POST', '/api/admin/workers', { name, url });
      const box = $('wk-new'); box.classList.remove('hidden');
      box.textContent = 'Copy now - the key is shown only once.\n\nROLE=worker\nMAIN_URL=' + (typeof MX_API_BASE === 'string' && MX_API_BASE ? MX_API_BASE : location.origin) + '\nWORKER_KEY=' + w.key + '\nPUBLIC_URL=' + (url || 'https://YOUR-WORKER-ADDRESS') + '\nALLOWED_ORIGINS=' + location.origin;
      $('wk-name').value = ''; $('wk-url').value = '';
      load();
    } catch (e) { alert('Failed: ' + (e.code || e.message)); }
  });

  // refresh while the Settings page is open and the admin is signed in
  setInterval(() => {
    const page = $('settings');
    if (!page || !page.classList.contains('active') || document.visibilityState === 'hidden') return;
    try { if (!firebase.auth().currentUser) return; } catch (e) { return; }
    load();
  }, 5000);
  document.addEventListener('click', (e) => { if (e.target.closest && e.target.closest('[data-page="settings"]')) setTimeout(load, 300); });
})();
