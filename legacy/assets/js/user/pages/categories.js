// --- EXPLORE LIST (Genres / Industries / Languages / Types) ---
window.exploreTab = window.exploreTab || 'genre';
window.setExploreTab = (x) => { window.exploreTab = x; renderCategoriesPage(); };
const renderCategoriesPage = () => {
    const box = document.getElementById('categories-grid');
    if (!box) return;
    document.querySelectorAll('#ex-tabs button').forEach(b => b.classList.toggle('on', b.dataset.x === exploreTab));
    const rel = mxList().map(e => e[1]);
    const cnt = (names, fn) => names.map(n => [n, rel.filter(m => fn(m, n)).length]).filter(([_, c]) => c > 0);
    const data = {
        genre: cnt(MX.GENRES, (m, n) => MX.genresOf(m).includes(n)),
        industry: cnt(MX.INDUSTRIES, (m, n) => m.industry === n),
        language: cnt(MX.LANGUAGES, (m, n) => MX.langsOf(m).includes(n)),
        type: cnt(MX.TYPES, (m, n) => MX.typeOf(m) === n)
    }[exploreTab];
    const q = (s) => s.replace(/'/g, "\\'");
    box.innerHTML = data.length
        ? data.map(([n, c]) => `<button class="ex-item" onclick="openBrowse({${exploreTab}:'${q(n)}'})"><span>${esc(n)}</span><small>${c}</small></button>`).join('')
        : '<p class="nf-empty">Nothing here yet.</p>';
};
