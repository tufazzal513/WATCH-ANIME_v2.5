// --- RENDER HOME PAGE (tabs: Home / Movies / Series / Anime) ---
window.homeTab = window.homeTab || 'home';
window.setHomeTab = (t) => { window.homeTab = t; renderHomePage(); window.scrollTo(0, 0); };
const renderHomePage = () => {
    const container = document.getElementById('movie-carousels-container');
    if (!container) return;
    const tab = homeTab;
    document.querySelectorAll('#home-chips button[data-t]').forEach(b => b.classList.toggle('on', b.dataset.t === tab));

    const all = Object.entries(allMovies);
    const rel = mxList().filter(([_, m]) => mxInTab(m, tab));
    const upcoming = all.filter(([_, m]) => !mxReleased(m) && mxInTab(m, tab));
    const pct = (id) => { const p = progressMap[id]; return p && p.d ? Math.min(100, Math.round(p.t / p.d * 100)) : 0; };

    const card = (id, m, o = {}) => `
        <div class="nf-card ${o.rank ? 'nf-top' : ''}" onclick="showMovieDetails('${id}')">
            ${o.rank ? `<span class="nf-rank">${o.rank}</span>` : ''}
            <div class="nf-poster" ${mxT(m)}>
                ${mxImg(m)}${mxBadges(m)}
                ${o.up ? '<div class="nf-soon"><i class="fas fa-clock"></i> Coming Soon</div>' : ''}
                ${o.prog ? `<div class="nf-prog"><i style="width:${pct(id)}%"></i></div>` : ''}
                <div class="nf-hover">
                    <div class="nf-hbtns"><span class="nf-play"><i class="fas fa-play"></i></span><span class="nf-plus ${inMyList(id) ? 'on' : ''}" data-mylist="${id}" onclick="toggleMyList('${id}', event)" title="My List"><i class="fas ${inMyList(id) ? 'fa-check' : 'fa-plus'}"></i></span></div>
                    <p class="nf-ht">${esc(m.title)}</p>
                    ${mxMeta(m)}
                </div>
            </div>
        </div>`;
    const row = (title, arr, o = {}) => {
        if (!arr.length) return '';
        const click = o.go ? ` onclick="${o.go}"` : '';
        return `<section class="nf-row"><h2 class="nf-rt"${click}>${esc(title)}${o.go ? ' <i class="fas fa-chevron-right"></i>' : ''}</h2><div class="nf-strip no-scrollbar">${arr.map(([id, m], i) => card(id, m, { rank: o.top ? i + 1 : 0, up: o.up, prog: o.prog })).join('')}</div></section>`;
    };
    const q = (s) => s.replace(/\\/g, '\\\\').replace(/'/g, "\\'");

    const cw = Object.entries(progressMap || {}).sort((a, b) => (b[1].at || 0) - (a[1].at || 0)).map(([id]) => [id, allMovies[id]]).filter(([_, m]) => m && mxInTab(m, tab));
    const mine = Object.keys(myListIds || {}).map(id => [id, allMovies[id]]).filter(([_, m]) => m && mxInTab(m, tab));
    const hot = mxSortHot(rel);
    const fresh = [...rel].sort((a, b) => mxWhen(b[1]) - mxWhen(a[1])).slice(0, 20);
    const top = [...rel].filter(([_, m]) => MX.ratingNum(m) > 0).sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1])).slice(0, 20);
    const nm = { home: '', movies: ' Movies', series: ' Series', anime: ' Anime' }[tab];

    let out = row('Continue Watching', cw, { prog: true }) + row('My List', mine)
            + row('Top 10' + nm + ' Today', hot.slice(0, 10), { top: true })
            + row('New Releases', fresh) + row('Top Rated', top) + row('Coming Soon', upcoming, { up: true });

    // by industry / language / genre (most titles first; only groups with 3+ titles)
    const groups = (keyFn) => { const g = {}; rel.forEach(e => keyFn(e[1]).forEach(k => (g[k] = g[k] || []).push(e))); return Object.entries(g).filter(([_, a]) => a.length >= 3).sort((a, b) => b[1].length - a[1].length); };
    groups(m => m.industry ? [m.industry] : []).slice(0, 4).forEach(([k, a]) => out += row('Popular in ' + k, mxSortHot(a).slice(0, 20), { go: `openBrowse({industry:'${q(k)}'})` }));
    groups(m => MX.langsOf(m)).slice(0, 2).forEach(([k, a]) => out += row('Watch in ' + k, mxSortHot(a).slice(0, 20), { go: `openBrowse({language:'${q(k)}'})` }));
    groups(m => MX.genresOf(m)).slice(0, 8).forEach(([k, a]) => out += row(k, mxSortHot(a).slice(0, 20), { go: `openBrowse({genre:'${q(k)}'})` }));

    container.innerHTML = out || '<p class="nf-empty">Nothing here yet.</p>';
};
