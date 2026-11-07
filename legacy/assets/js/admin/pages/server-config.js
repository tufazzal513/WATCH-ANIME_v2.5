// Admin -> Settings -> "Server Settings": edit the main server's configuration (no environment variables needed).
(function () {
  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  let fields = [];
  const msg = (t, ok) => { const m = $('cfg-msg'); if (m) { m.className = 'text-xs mt-3 ' + (ok ? 'text-emerald-400' : 'text-red-400'); m.textContent = t; } };
  const badge = (f) => f.source === 'panel' ? '<span class="px-1.5 py-0.5 rounded bg-emerald-600/30 text-emerald-300 text-[10px] font-bold">saved here</span>'
    : f.source === 'env' ? '<span class="px-1.5 py-0.5 rounded bg-gray-600/50 text-gray-300 text-[10px] font-bold">from host</span>'
    : '<span class="px-1.5 py-0.5 rounded bg-gray-700 text-gray-400 text-[10px]">default</span>';

  function draw() {
    const groups = [];
    fields.forEach((f) => { let g = groups.find((x) => x.name === f.group); if (!g) groups.push(g = { name: f.group, items: [] }); g.items.push(f); });
    $('cfg-box').innerHTML = groups.map((g) =>
      '<div><h4 class="text-sm font-bold text-gray-200 mb-2">' + esc(g.name) + '</h4><div class="space-y-3">' +
      g.items.map((f) => {
        let input;
        if (f.type === 'bool') input = '<label class="inline-flex items-center gap-2 text-sm text-gray-200"><input type="checkbox" data-k="' + f.key + '" ' + (f.value === '1' ? 'checked' : '') + '> Enabled</label>';
        else if (f.secret) input = '<input data-k="' + f.key + '" type="password" autocomplete="new-password" placeholder="' + (f.set ? 'Saved (' + esc(f.hint) + ') - type to replace' : 'Not set') + '" class="w-full bg-gray-700 text-white p-2 rounded-lg border border-gray-600 outline-none text-sm">';
        else input = '<input data-k="' + f.key + '" type="' + (f.type === 'number' ? 'number' : 'text') + '" value="' + esc(f.value) + '" class="w-full bg-gray-700 text-white p-2 rounded-lg border border-gray-600 outline-none text-sm">';
        return '<div><div class="flex items-center justify-between gap-2 mb-1"><label class="text-xs text-gray-300 font-medium">' + esc(f.label) + '</label><span>' + badge(f) +
          (f.source === 'panel' ? ' <button type="button" data-reset="' + f.key + '" class="text-[10px] text-red-400 hover:text-red-300 ml-1">reset</button>' : '') + '</span></div>' + input +
          (f.help ? '<p class="text-[10px] text-gray-500 mt-1">' + esc(f.help) + '</p>' : '') + '</div>';
      }).join('') + '</div></div>').join('');
  }

  async function load() {
    try { fields = (await mxApi('GET', '/api/admin/config')).fields || []; draw(); }
    catch (e) { const b = $('cfg-box'); if (b) b.innerHTML = '<p class="text-xs text-red-400">Could not load settings (' + esc(e.code || e.message) + ').</p>'; }
  }

  function changes() {
    const out = {};
    fields.forEach((f) => {
      const el = document.querySelector('#cfg-box [data-k="' + f.key + '"]'); if (!el) return;
      if (f.type === 'bool') { const v = el.checked ? '1' : '0'; if (v !== (f.value || '0')) out[f.key] = v; }
      else if (f.secret) { if (el.value.trim()) out[f.key] = el.value.trim(); }
      else if (el.value.trim() !== (f.value || '')) out[f.key] = el.value.trim();
    });
    return out;
  }

  document.addEventListener('click', async (ev) => {
    const t = ev.target.closest && ev.target.closest('button'); if (!t) return;
    if (t.id === 'cfg-save') {
      const values = changes();
      if (!Object.keys(values).length) return msg('Nothing changed.', true);
      t.disabled = true;
      try { const r = await mxApi('PUT', '/api/admin/config', { values }); fields = r.fields; draw(); msg('Saved and applied: ' + r.changed.join(', '), true); }
      catch (e) { const er = e.body && e.body.errors; msg('Not saved: ' + (er ? Object.keys(er).map((k) => k + ' - ' + er[k]).join('; ') : (e.code || e.message)), false); }
      t.disabled = false;
    } else if (t.dataset && t.dataset.reset) {
      if (!confirm('Remove the value saved here? The host\'s variable (or the default) will be used again.')) return;
      try { const v = {}; v[t.dataset.reset] = ''; fields = (await mxApi('PUT', '/api/admin/config', { values: v })).fields; draw(); msg('Reset.', true); }
      catch (e) { msg('Failed: ' + (e.code || e.message), false); }
    } else if (t.id === 'cfg-test') {
      t.disabled = true; msg('Testing...', true);
      try { const r = await mxApi('POST', '/api/admin/config/test-telegram', {}); msg((r.mode ? '[' + r.mode + '] ' : '') + r.message, !!r.ok); }
      catch (e) { msg('Test failed: ' + (e.code || e.message), false); }
      t.disabled = false;
    }
  });

  document.addEventListener('click', (e) => { if (e.target.closest && e.target.closest('[data-page="settings"]')) setTimeout(load, 300); });
  setTimeout(() => { try { if (firebase.auth().currentUser) load(); } catch (e) {} }, 1500);
})();
