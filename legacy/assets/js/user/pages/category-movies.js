// --- BROWSE PAGE: filtered titles, Netflix-style rows + All Titles grid ---
const BR_DEF = { genre: '', industry: '', language: '', type: '', year: '', tab: 'all', sort: 'suggested' };
let browse = { ...BR_DEF };
window.openBrowse = (f) => { browse = { ...BR_DEF, ...(f || {}) }; document.getElementById('br-sort').value = browse.sort; renderBrowse(); showPage('category-movies-page'); window.scrollTo(0, 0); };
window.openCategoryMovies = (name) => openBrowse({ genre: name });
window.setBrowseTab = (t) => { browse.tab = t; renderBrowse(); };
window.setBrowseSort = (s) => { browse.sort = s; renderBrowse(); };

const browseMatches = () => mxList().filter(([_, m]) =>
    (!browse.genre || MX.genresOf(m).includes(browse.genre)) &&
    (!browse.industry || m.industry === browse.industry) &&
    (!browse.language || MX.langsOf(m).includes(browse.language)) &&
    (!browse.type || MX.typeOf(m) === browse.type) &&
    (!browse.year || String(m.year) === String(browse.year)) &&
    mxInTab(m, browse.tab));

const renderBrowse = () => {
    const res = browseMatches();
    const label = [browse.genre, browse.industry, browse.language, browse.type, browse.year].filter(Boolean);
    document.getElementById('selected-category-title').textContent = label.length ? label.join(' · ') : 'All Titles';
    document.querySelectorAll('#br-tabs button').forEach(b => b.classList.toggle('on', b.dataset.b === browse.tab));
    const sorted = { suggested: () => mxSortHot(res), new: () => [...res].sort((a, b) => mxWhen(b[1]) - mxWhen(a[1])), top: () => [...res].sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1])), az: () => [...res].sort((a, b) => String(a[1].title).localeCompare(String(b[1].title))) }[browse.sort]();

    // Netflix-style rows (posters only) when there are enough titles
    const rowsBox = document.getElementById('br-rows'), name = label[0] || 'this list';
    const strip = (title, arr) => `<section class="nf-row"><h2 class="nf-rt">${esc(title)}</h2><div class="nf-strip no-scrollbar">${arr.map(([id, m]) => `<div class="nf-card" onclick="showMovieDetails('${id}')"><div class="nf-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}</div></div>`).join('')}</div></section>`;
    if (res.length >= 8 && browse.sort === 'suggested') {
        const top = [...res].filter(([_, m]) => MX.ratingNum(m) > 0).sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1])).slice(0, 15);
        const fresh = [...res].sort((a, b) => mxWhen(b[1]) - mxWhen(a[1])).slice(0, 15);
        rowsBox.innerHTML = strip('Top Rated in ' + name, top) + strip('New in ' + name, fresh);
    } else rowsBox.innerHTML = '';
    document.getElementById('br-all-title').classList.toggle('hidden', !rowsBox.innerHTML);

    const grid = document.getElementById('category-movies-grid');
    if (sorted.length) mxChunk(grid, sorted, ([id, m]) => mxGridCard(id, m), 24);
    else grid.innerHTML = '<div class="nf-empty" style="grid-column:1/-1"><i class="fas fa-film" style="font-size:38px;display:block;margin-bottom:10px"></i>No titles match these filters.</div>';
};

// ---- filter sheet ----
const brOpts = (vals) => '<option value="">All</option>' + vals.map(v => `<option value="${esc(v)}">${esc(v)}</option>`).join('');
window.openBrowseFilters = () => {
    const rel = mxList().map(e => e[1]);
    const years = [...new Set(rel.map(m => m.year).filter(Boolean))].sort().reverse();
    const has = (list, fn) => list.filter(x => rel.some(m => fn(m, x)));
    const fld = (id, label, vals) => `<label class="mx-fld"><span>${label}</span><select id="${id}">${brOpts(vals)}</select></label>`;
    document.getElementById('br-fields').innerHTML =
        fld('bf-type', 'Type', has(MX.TYPES, (m, x) => MX.typeOf(m) === x)) + fld('bf-industry', 'Industry / Country', has(MX.INDUSTRIES, (m, x) => m.industry === x)) +
        fld('bf-genre', 'Genre', has(MX.GENRES, (m, x) => MX.genresOf(m).includes(x))) + fld('bf-language', 'Language', has(MX.LANGUAGES, (m, x) => MX.langsOf(m).includes(x))) + fld('bf-year', 'Year', years);
    ['type', 'industry', 'genre', 'language', 'year'].forEach(k => { document.getElementById('bf-' + k).value = browse[k]; });
    document.getElementById('br-sheet').classList.remove('hidden');
};
window.closeBrowseFilters = () => document.getElementById('br-sheet').classList.add('hidden');
window.applyBrowseFilters = () => { ['type', 'industry', 'genre', 'language', 'year'].forEach(k => { browse[k] = document.getElementById('bf-' + k).value; }); closeBrowseFilters(); renderBrowse(); };
window.resetBrowseFilters = () => { browse = { ...BR_DEF, sort: browse.sort, tab: browse.tab }; closeBrowseFilters(); renderBrowse(); };
