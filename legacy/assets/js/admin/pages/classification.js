// --- ADMIN: Classification picker (Type, Industry, Language, Dubbed, Genres with search) ---
(function () {
  const $ = (id) => document.getElementById(id);
  const typeSel = $('movie-type'), indSel = $('movie-industry');
  if (!typeSel || !indSel || !window.MX) return;
  const esc = (t) => String(t == null ? '' : t).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

  typeSel.innerHTML = MX.TYPES.map(t => `<option value="${esc(t)}">${esc(t)}</option>`).join('');
  indSel.innerHTML = '<option value="">— Not set —</option>' + MX.INDUSTRIES.map(t => `<option value="${esc(t)}">${esc(t)}</option>`).join('');

  let langs = [], genres = [];

  // ----- languages (chips; popular first) -----
  const langBox = $('mx-langs');
  const langOrder = [...MX.POPULAR_LANGUAGES, ...MX.LANGUAGES.filter(l => !MX.POPULAR_LANGUAGES.includes(l))];
  const renderLangs = () => {
    langBox.innerHTML = langOrder.map(l => `<button type="button" class="mx-chip ${langs.includes(l) ? 'on' : ''}" data-l="${esc(l)}">${esc(l)}</button>`).join('');
  };
  langBox.addEventListener('click', (e) => {
    const b = e.target.closest('[data-l]'); if (!b) return;
    const l = b.dataset.l; langs = langs.includes(l) ? langs.filter(x => x !== l) : [...langs, l]; renderLangs();
  });

  // ----- genres (selected chips + popular + search + checkbox list) -----
  const sBox = $('mx-gsel'), pBox = $('mx-gpop'), lBox = $('mx-glist'), q = $('mx-gsearch');
  const toggle = (g) => { genres = genres.includes(g) ? genres.filter(x => x !== g) : [...genres, g]; renderGenres(); };
  const renderGenres = () => {
    sBox.innerHTML = genres.length
      ? genres.map(g => `<span class="mx-chip on">${esc(g)} <button type="button" data-rm="${esc(g)}" aria-label="Remove">×</button></span>`).join('')
      : '<span class="text-xs text-gray-500">No genre selected yet</span>';
    pBox.innerHTML = MX.POPULAR_GENRES.map(g => `<button type="button" class="mx-chip ${genres.includes(g) ? 'on' : ''}" data-g="${esc(g)}">${esc(g)}</button>`).join('');
    const term = q.value.trim().toLowerCase();
    const list = MX.GENRES.filter(g => !term || g.toLowerCase().includes(term));
    lBox.innerHTML = list.length
      ? list.map(g => `<label class="mx-row"><input type="checkbox" data-g="${esc(g)}" ${genres.includes(g) ? 'checked' : ''}> <span>${esc(g)}</span></label>`).join('')
      : '<p class="text-xs text-gray-500 p-2">No genre matches "' + esc(q.value) + '"</p>';
  };
  sBox.addEventListener('click', (e) => { const b = e.target.closest('[data-rm]'); if (b) toggle(b.dataset.rm); });
  pBox.addEventListener('click', (e) => { const b = e.target.closest('[data-g]'); if (b) toggle(b.dataset.g); });
  lBox.addEventListener('change', (e) => { const c = e.target.closest('[data-g]'); if (c) toggle(c.dataset.g); });
  q.addEventListener('input', renderGenres);
  q.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    const term = q.value.trim().toLowerCase();
    const hit = MX.GENRES.find(g => g.toLowerCase().includes(term) && !genres.includes(g));
    if (hit) { genres = [...genres, hit]; q.value = ''; renderGenres(); }
  });
  $('mx-gclear').addEventListener('click', () => { genres = []; q.value = ''; renderGenres(); });

  // keep Type in step with the Movie/Series switch (only while Type is Movie/Series)
  document.querySelectorAll('input[name="content-type"]').forEach(r => r.addEventListener('change', () => {
    if (['Movie', 'Series'].includes(typeSel.value)) typeSel.value = r.value === 'series' ? 'Series' : 'Movie';
  }));

  window.mxCls = {
    set(m) {
      m = m || {};
      typeSel.value = MX.typeOf(m);
      indSel.value = m.industry || '';
      langs = MX.langsOf(m).slice();
      genres = MX.genresOf(m).slice();
      $('movie-dubbed').checked = !!m.dubbed;
      q.value = ''; renderLangs(); renderGenres();
    },
    reset() { this.set({}); typeSel.value = 'Movie'; },
    get() {
      return {
        type: typeSel.value || null,
        industry: indSel.value || null,
        languages: langs.length ? langs : null,
        dubbed: $('movie-dubbed').checked,
        genres: genres.length ? genres : null,
        category: genres[0] || ''      // primary genre, kept for older code
      };
    },
    genres: () => genres.slice()
  };
  window.mxCls.reset();
})();
