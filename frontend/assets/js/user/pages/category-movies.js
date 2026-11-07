// --- BROWSE PAGE: server-filtered titles, Netflix-style rows + All Titles grid ---
const BR_DEF = { genre: '', industry: '', language: '', type: '', year: '', upcoming: 'no', tab: 'all', sort: 'suggested' };
let browse = { ...BR_DEF };
let browseRequestId = 0;
window.openBrowse = (f) => { browse = { ...BR_DEF, ...(f || {}) }; document.getElementById('br-sort').value = browse.sort; renderBrowse(); showPage('category-movies-page'); window.scrollTo(0, 0); };
window.openCategoryMovies = (name) => openBrowse({ genre: name });
window.setBrowseTab = (t) => { browse.tab = t; renderBrowse(); };
window.setBrowseSort = (s) => { browse.sort = s; renderBrowse(); };

const browseQuery = () => {
    // The old UI applied both the selected tab and the filter sheet. If those
    // constraints cannot both be represented by the API's single `type`, the
    // intersection is empty and must not silently broaden the result.
    if (browse.tab === 'anime' && browse.type && browse.type !== 'Anime') return null;
    const sort = ({ suggested: 'hot', new: 'new', top: 'rating', az: 'title' })[browse.sort] || 'hot';
    const type = browse.type || (browse.tab === 'anime' ? 'Anime' : '');
    return {
        category: browse.genre,
        industry: browse.industry,
        language: browse.language,
        type: type,
        year: browse.year,
        contentType: browse.tab === 'movies' ? 'movie' : (browse.tab === 'series' ? 'series' : ''),
        upcoming: browse.upcoming,
        sort: sort
    };
};

const renderBrowse = () => {
    const requestId = ++browseRequestId;
    const label = [browse.genre, browse.industry, browse.language, browse.type, browse.year,
        browse.upcoming === 'yes' ? 'Upcoming' : ''].filter(Boolean);
    document.getElementById('selected-category-title').textContent = label.length ? label.join(' · ') : 'All Titles';
    document.querySelectorAll('#br-tabs button').forEach(b => b.classList.toggle('on', b.dataset.b === browse.tab));
    const rowsBox = document.getElementById('br-rows');
    const grid = document.getElementById('category-movies-grid');
    rowsBox.innerHTML = '';
    grid.innerHTML = '<div class="nf-empty" style="grid-column:1/-1">Loading titles…</div>';

    const params = browseQuery();
    const load = params === null ? Promise.resolve([]) : MX_API.queryMovies(params);
    load.then(items => {
        if (requestId !== browseRequestId) return;
        const res = (Array.isArray(items) ? items : []).filter(m => m && m.id).map(m => [m.id, m]);
        // Server owns filtering and ordering. These local slices only build the
        // existing visual recommendation rows from the already-filtered set.
        const sorted = res;
        const name = label[0] || 'this list';
        const strip = (title, arr) => `<section class="nf-row"><h2 class="nf-rt">${esc(title)}</h2><div class="nf-strip no-scrollbar">${arr.map(([id, m]) => `<div class="nf-card" onclick="showMovieDetails('${id}')"><div class="nf-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}</div></div>`).join('')}</div></section>`;
        if (res.length >= 8 && browse.sort === 'suggested') {
            const top = [...res].filter(([_, m]) => MX.ratingNum(m) > 0).sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1])).slice(0, 15);
            const fresh = [...res].sort((a, b) => mxWhen(b[1]) - mxWhen(a[1])).slice(0, 15);
            rowsBox.innerHTML = strip('Top Rated in ' + name, top) + strip('New in ' + name, fresh);
        }
        document.getElementById('br-all-title').classList.toggle('hidden', !rowsBox.innerHTML);
        if (sorted.length) mxChunk(grid, sorted, ([id, m]) => mxGridCard(id, m), 24);
        else grid.innerHTML = '<div class="nf-empty" style="grid-column:1/-1"><i class="fas fa-film" style="font-size:38px;display:block;margin-bottom:10px"></i>No titles match these filters.</div>';
    }).catch(error => {
        if (requestId !== browseRequestId) return;
        console.error('Browse catalog request failed:', error);
        rowsBox.innerHTML = '';
        document.getElementById('br-all-title').classList.add('hidden');
        grid.innerHTML = '<div class="nf-empty" style="grid-column:1/-1">Could not load titles. Check your connection and try again.</div>';
    });
};

// ---- filter sheet ----
const brOpts = (vals) => '<option value="">All</option>' + vals.map(v => `<option value="${esc(v)}">${esc(v)}</option>`).join('');
window.openBrowseFilters = () => {
    const catalog = Object.values(allMovies || {});
    const years = [...new Set(catalog.map(m => m && m.year).filter(Boolean))].sort((a, b) => Number(b) - Number(a));
    const has = (list, fn) => list.filter(x => catalog.some(m => m && fn(m, x)));
    const fld = (id, label, vals) => `<label class="mx-fld"><span>${label}</span><select id="${id}">${brOpts(vals)}</select></label>`;
    document.getElementById('br-fields').innerHTML =
        fld('bf-type', 'Type', has(MX.TYPES, (m, x) => MX.typeOf(m) === x)) +
        fld('bf-industry', 'Industry / Country', has(MX.INDUSTRIES, (m, x) => m.industry === x)) +
        fld('bf-genre', 'Genre', has(MX.GENRES, (m, x) => MX.genresOf(m).includes(x))) +
        fld('bf-language', 'Language', has(MX.LANGUAGES, (m, x) => MX.langsOf(m).includes(x))) +
        fld('bf-year', 'Year', years) +
        '<label class="mx-fld"><span>Release</span><select id="bf-upcoming"><option value="no">Released</option><option value="yes">Upcoming</option><option value="">All</option></select></label>';
    ['type', 'industry', 'genre', 'language', 'year'].forEach(k => { document.getElementById('bf-' + k).value = browse[k]; });
    document.getElementById('bf-upcoming').value = browse.upcoming;
    document.getElementById('br-sheet').classList.remove('hidden');
};
window.closeBrowseFilters = () => document.getElementById('br-sheet').classList.add('hidden');
window.applyBrowseFilters = () => { ['type', 'industry', 'genre', 'language', 'year', 'upcoming'].forEach(k => { browse[k] = document.getElementById('bf-' + k).value; }); closeBrowseFilters(); renderBrowse(); };
window.resetBrowseFilters = () => { browse = { ...BR_DEF, sort: browse.sort, tab: browse.tab }; closeBrowseFilters(); renderBrowse(); };
