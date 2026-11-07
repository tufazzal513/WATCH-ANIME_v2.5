// --- SEARCH: MovieBox-style (recent, everyone is searching, hot lists, tabs, list results, voice) ---
const searchInput = document.getElementById('search-input');
const searchClearBtn = document.getElementById('search-clear-btn');
const searchGoBtn = document.getElementById('search-go-btn');
const suggestionsDiv = document.getElementById('search-suggestions');
let searchTimeout, searchTab = 'all', lastResults = [], lastTerm = '', popularTerms = [], recentOpen = false, hotTab = 'movies', searchRequestID = 0, suggestionRequestID = 0;

const termsOf = (s) => s.toLowerCase().split(/\s+/).filter(Boolean);
const hay = (m) => `${m.title} ${m.cast || ''} ${MX.genresOf(m).join(' ')} ${m.industry || ''} ${MX.langsOf(m).join(' ')} ${MX.typeOf(m)} ${m.year || ''}`.toLowerCase();
function matchMovie(m, terms, currentYear) {
    const text = hay(m);
    return terms.every(t => {
        if ((t === 'new' || t === 'latest') && parseInt(m.year) === currentYear) return true;
        if ((t === 'trending' || t === 'popular' || t === 'hot') && MX.ratingNum(m) >= 7) return true;
        return text.includes(t);
    });
}
const searchPool = (term) => {
    const terms = termsOf(term), y = new Date().getFullYear(), lt = term.toLowerCase().trim();
    const score = (m) => { const t = String(m.title).toLowerCase(); return t === lt ? 3 : t.startsWith(lt) ? 2 : t.includes(lt) ? 1 : 0; };
    return mxList().filter(([_, m]) => matchMovie(m, terms, y))
        .sort((a, b) => score(b[1]) - score(a[1]) || mxHot(b[0], b[1]) - mxHot(a[0], a[1]));
};
const hl = (text, terms) => {
    let out = esc(text);
    terms.filter(t => t.length > 1).forEach(t => { out = out.replace(new RegExp('(' + t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ')', 'ig'), '<mark>$1</mark>'); });
    return out;
};

// ---------- empty state ----------
function renderSearchHistory() {
    document.getElementById('search-home').classList.remove('hidden');
    document.getElementById('search-view').classList.add('hidden');
    suggestionsDiv.classList.add('hidden');
    const box = document.getElementById('search-history-container'), chips = document.getElementById('search-history-chips');
    box.classList.toggle('hidden', !searchHistory.length);
    chips.innerHTML = searchHistory.map(t => `<button class="sr-chip" data-term="${esc(t)}">${esc(t)}</button>`).join('');
    chips.classList.toggle('sr-collapsed', !recentOpen);
    const more = document.getElementById('sr-more');
    more.classList.toggle('hidden', searchHistory.length < 4);
    more.querySelector('i').className = 'fas fa-chevron-' + (recentOpen ? 'up' : 'down');
    renderPopular(); renderHot();
}
window.toggleRecent = () => { recentOpen = !recentOpen; renderSearchHistory(); };
function addToHistory(term) {
    if (!term.trim()) return;
    searchHistory = searchHistory.filter(t => t.toLowerCase() !== term.toLowerCase());
    searchHistory.unshift(term);
    if (searchHistory.length > 12) searchHistory.pop();
    localStorage.setItem('movex_search_history', JSON.stringify(searchHistory));
}
window.clearSearchHistory = function () { searchHistory = []; localStorage.removeItem('movex_search_history'); renderSearchHistory(); };

// "Everyone is searching": real searches from all users (falls back to the hottest titles until data exists)
function renderPopular() {
    const box = document.getElementById('search-popular'), chips = document.getElementById('search-popular-chips');
    let list = popularTerms.slice(0, 10);
    if (!list.length) list = mxSortHot(mxList()).slice(0, 8).map(e => e[1].title);
    box.classList.toggle('hidden', !list.length);
    chips.innerHTML = list.map(t => `<button class="sr-chip" data-term="${esc(t)}">${esc(t)}</button>`).join('');
}
function loadPopular() {
    MX_API.searchSuggest('').then(data => {
        popularTerms = (data && data.items || []).map(item => item.term).filter(Boolean);
        renderPopular();
    }).catch(() => {});
}
function logSearchTerm(term, count) {
    const t = term.trim();
    if (!count || t.length < 2 || t.length > 100 || !auth.currentUser) return;
    const key = t.toLowerCase().replace(/\s+/g, ' ').slice(0, 40);
    const seen = sessionStorage.getItem('sq_' + key);
    if (seen) return;
    sessionStorage.setItem('sq_' + key, '1');
    MX_API.searchHit(t.slice(0, 100)).catch(() => {});
}

// Hot lists (ranked 1-5)
window.setHotTab = (h) => { hotTab = h; renderHot(); };
function renderHot() {
    document.querySelectorAll('#hot-tabs button').forEach(b => b.classList.toggle('on', b.dataset.h === hotTab));
    const list = mxSortHot(mxList().filter(([_, m]) => mxInTab(m, hotTab))).slice(0, 5);
    document.getElementById('top-searches').innerHTML = list.length ? list.map(([id, m], i) => `
        <div class="sr-row" onclick="selectSuggestion('${id}', '')">
            <div class="sr-poster" ${mxT(m)}>${mxImg(m)}<span class="sr-rank r${i + 1}">${i + 1}</span></div>
            <div class="sr-info"><p class="sr-title">${esc(m.title)}</p>${mxMeta(m)}</div>
        </div>`).join('') : '<p class="nf-empty">Nothing here yet.</p>';
}

// ---------- suggestions while typing ----------
function showSuggestions(term) {
    term = String(term || '').trim();
    const requestID = ++suggestionRequestID;
    if (term.length < 2) { suggestionsDiv.classList.add('hidden'); return; }
    const phrases = popularTerms.filter(p => p.toLowerCase().includes(term.toLowerCase()) && p.toLowerCase() !== term.toLowerCase()).slice(0, 3);
    const render = (titles) => {
        if (requestID !== suggestionRequestID || searchInput.value.trim() !== term) return;
        const unique = new Map();
        (Array.isArray(titles) ? titles : []).forEach(m => { if (m && m.id && m.title && !unique.has(m.id)) unique.set(m.id, m); });
        const rows = Array.from(unique.values()).slice(0, 5);
        if (!phrases.length && !rows.length) { suggestionsDiv.classList.add('hidden'); return; }
        suggestionsDiv.innerHTML =
            phrases.map(p => `<div class="sr-sp" data-term="${esc(p)}"><i class="fas fa-search"></i><span>${hl(p, termsOf(term))}</span></div>`).join('') +
            rows.map(m => {
                allMovies[m.id] = m;
                return `<div class="sr-srow" onclick="selectSuggestion('${String(m.id).replace(/'/g, "\\'")}', '')"><div class="sr-sposter" ${mxT(m)}>${mxImg(m)}</div><div><div class="sr-st">${hl(m.title, termsOf(term))}</div>${mxMeta(m, false)}</div></div>`;
            }).join('');
        suggestionsDiv.classList.remove('hidden');
    };
    // Show shared popular phrases immediately while the server title search is in flight.
    render([]);
    MX_API.movieSuggestions(term, 5).then(data => {
        render(data && Array.isArray(data.items) ? data.items : []);
    }).catch(err => {
        if (requestID !== suggestionRequestID) return;
        console.warn('MoveX title suggestions unavailable; using local catalog:', err);
        render(searchPool(term).slice(0, 5).map(entry => entry[1]));
    });
}
window.selectSuggestion = (id, title) => {
    if (title) addToHistory(title); else if (lastTerm) addToHistory(lastTerm);
    showMovieDetails(id);
    suggestionsDiv.classList.add('hidden');
};
window.clickSearchResult = (id, title) => { addToHistory(title); showMovieDetails(id); };

// ---------- committed search: tabs + list ----------
function commitSearch(term) {
    term = (term || '').trim();
    if (!term) return;
    searchInput.value = term; lastTerm = term;
    searchClearBtn.classList.remove('hidden'); searchGoBtn.classList.add('hidden');
    suggestionsDiv.classList.add('hidden'); searchInput.blur();
    addToHistory(term);
    searchTab = 'all';
    const requestID = ++searchRequestID;
    const list = document.getElementById('search-results');
    const related = document.getElementById('sr-related');
    document.getElementById('search-home').classList.add('hidden');
    document.getElementById('search-view').classList.remove('hidden');
    document.getElementById('sr-tabs').innerHTML = '';
    related.classList.add('hidden');
    list.innerHTML = '<div class="nf-empty" role="status" aria-live="polite">Searching...</div>';
    // These legacy keywords have special client-side meaning (current-year / rating),
    // not literal catalog text. Preserve that behavior rather than sending them as plain q.
    const semanticTerms = termsOf(term);
    if (semanticTerms.some(t => ['new', 'latest', 'trending', 'popular', 'hot'].includes(t))) {
        lastResults = searchPool(term);
        logSearchTerm(term, lastResults.length);
        renderSearchResults();
        return;
    }
    MX_API.queryMovies({ q: term, sort: 'relevance' }).then(items => {
        if (requestID !== searchRequestID || term !== lastTerm) return;
        lastResults = (Array.isArray(items) ? items : []).filter(m => m && m.id).map(m => [m.id, m]);
        // Keep the shared catalog cache warm for details, recommendations and cross-page navigation.
        lastResults.forEach(([id, movie]) => { allMovies[id] = movie; });
        logSearchTerm(term, lastResults.length);
        renderSearchResults();
    }).catch(err => {
        if (requestID !== searchRequestID || term !== lastTerm) return;
        console.error('MoveX server search failed:', err);
        // Preserve usable results when the API is temporarily unavailable.
        lastResults = searchPool(term);
        renderSearchResults();
        if (!lastResults.length) list.insertAdjacentHTML('afterbegin', '<p class="nf-empty" role="status">Search service is temporarily unavailable. Please try again.</p>');
    });
}
window.performSearch = commitSearch;

function tabCounts() {
    const tabs = [['all', 'All', lastResults.length]];
    [['movies', 'Movies'], ['series', 'Series'], ['anime', 'Anime']].forEach(([k, l]) => { const c = lastResults.filter(([_, m]) => MX.tabsOf(m).includes(k)).length; if (c) tabs.push([k, l, c]); });
    const g = {}; lastResults.forEach(([_, m]) => MX.genresOf(m).forEach(x => g[x] = (g[x] || 0) + 1));
    Object.entries(g).sort((a, b) => b[1] - a[1]).slice(0, 6).forEach(([k, c]) => tabs.push(['g:' + k, k, c]));
    return tabs;
}
function renderSearchResults() {
    document.getElementById('search-home').classList.add('hidden');
    document.getElementById('search-view').classList.remove('hidden');
    const terms = termsOf(lastTerm), tabs = tabCounts();
    document.getElementById('sr-tabs').innerHTML = tabs.map(([k, l]) => `<button data-k="${esc(k)}" class="${k === searchTab ? 'on' : ''}">${esc(l)}</button>`).join('');
    const shown = lastResults.filter(([_, m]) => searchTab === 'all' || (searchTab.startsWith('g:') ? MX.genresOf(m).includes(searchTab.slice(2)) : MX.tabsOf(m).includes(searchTab)));
    const list = document.getElementById('search-results'), rel = document.getElementById('sr-related');
    if (shown.length) {
        mxChunk(list, shown, ([id, m]) => `
            <div class="sr-row" onclick="clickSearchResult('${id}', '${esc(m.title).replace(/'/g, "\\'")}')">
                <div class="sr-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}</div>
                <div class="sr-info"><p class="sr-title">${hl(m.title, terms)}</p>${mxMeta(m)}</div>
                <span class="ml-play"><i class="fas fa-play"></i></span>
            </div>`, 20);
        // Related Collection: other titles sharing genres with the top results
        const ids = new Set(lastResults.map(e => e[0])), top = shown.slice(0, 3).flatMap(e => MX.genresOf(e[1]));
        const relList = mxSortHot(mxList().filter(([id, m]) => !ids.has(id) && MX.genresOf(m).some(g => top.includes(g)))).slice(0, 12);
        rel.classList.toggle('hidden', !relList.length);
        rel.innerHTML = relList.length ? `<h3 class="br-h3">Related Collection</h3><div class="nf-strip no-scrollbar" style="padding:0 16px 20px">${relList.map(([id, m]) => `<div class="nf-card" onclick="showMovieDetails('${id}')"><div class="nf-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}</div></div>`).join('')}</div>` : '';
    } else {
        rel.classList.add('hidden');
        const hot = mxSortHot(mxList()).slice(0, 5);
        list.innerHTML = `<div class="nf-empty"><i class="fas fa-search" style="font-size:38px;display:block;margin-bottom:10px"></i>No results for "${esc(lastTerm)}"<br><small>Check the spelling or try another word.</small></div>` +
            (hot.length ? '<h3 class="br-h3">Try these</h3>' + hot.map(([id, m]) => `<div class="sr-row" onclick="showMovieDetails('${id}')"><div class="sr-poster" ${mxT(m)}>${mxImg(m)}</div><div class="sr-info"><p class="sr-title">${esc(m.title)}</p>${mxMeta(m)}</div></div>`).join('') : '');
    }
}

// ---------- events ----------
document.getElementById('sr-tabs').addEventListener('click', (e) => { const b = e.target.closest('[data-k]'); if (b) { searchTab = b.dataset.k; renderSearchResults(); } });
document.getElementById('search-page').addEventListener('click', (e) => { const c = e.target.closest('[data-term]'); if (c) commitSearch(c.dataset.term); });
searchInput.addEventListener('input', (e) => {
    const term = e.target.value, has = term.length > 0;
    searchClearBtn.classList.toggle('hidden', !has); searchGoBtn.classList.toggle('hidden', !has);
    clearTimeout(searchTimeout);
    searchTimeout = setTimeout(() => { if (!has) renderSearchHistory(); else showSuggestions(term); }, 200);
});
searchInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); commitSearch(searchInput.value); } });
searchGoBtn.addEventListener('click', () => commitSearch(searchInput.value));
searchClearBtn.addEventListener('click', () => { searchInput.value = ''; searchClearBtn.classList.add('hidden'); searchGoBtn.classList.add('hidden'); renderSearchHistory(); searchInput.focus(); });
searchInput.addEventListener('focus', () => { if (searchInput.value.trim()) searchGoBtn.classList.remove('hidden'); });
document.addEventListener('click', (e) => { if (!suggestionsDiv.contains(e.target) && !searchInput.contains(e.target)) suggestionsDiv.classList.add('hidden'); });

// voice search (Chrome/Android; hidden where unsupported)
const SR = window.SpeechRecognition || window.webkitSpeechRecognition, micBtn = document.getElementById('search-mic-btn');
if (SR) {
    micBtn.classList.remove('hidden');
    micBtn.addEventListener('click', () => {
        const r = new SR(); r.lang = navigator.language || 'en-US'; r.interimResults = false;
        micBtn.classList.add('on');
        r.onresult = (ev) => commitSearch(ev.results[0][0].transcript);
        r.onend = r.onerror = () => micBtn.classList.remove('on');
        try { r.start(); } catch (e) { micBtn.classList.remove('on'); }
    });
}
window.addEventListener('load', loadPopular);
auth.onAuthStateChanged(u => { if (u) loadPopular(); });

window.resetSearchPage = () => { suggestionRequestID++; searchRequestID++; searchInput.value = ''; searchClearBtn.classList.add('hidden'); searchGoBtn.classList.add('hidden'); lastTerm = ''; renderSearchHistory(); };
