// --- SHARED UI HELPERS (cards, badges, meta line, ranking) ---
const esc = (t) => String(t == null ? '' : t).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
window.mxStats = window.mxStats || {};          // { movieId: {plays, lists} } (read from Firebase 'stats')

// broken poster -> neat placeholder with the first letter of the title
window.mxImgFail = (img) => {
    const p = img.parentElement; img.remove();
    if (p && !p.querySelector('.mx-ph')) { const d = document.createElement('div'); d.className = 'mx-ph'; d.textContent = ((p.dataset.t || '?').trim().charAt(0) || '?').toUpperCase(); p.prepend(d); }
};
const mxImg = (m) => `<img src="${esc(m.posterUrl)}" loading="lazy" alt="" onerror="mxImgFail(this)">`;
const mxT = (m) => `data-t="${esc(m.title)}"`;
const mxReleased = (m) => { if (!m.isUpcoming) return true; const ms = getReleaseMs(m); return !!ms && ms <= Date.now(); };
const mxWhen = (m) => getReleaseMs(m) || (parseInt(m.year) ? Date.UTC(parseInt(m.year), 0, 1) : 0);
const mxList = () => Object.entries(allMovies).filter(([_, m]) => mxReleased(m));
const mxInTab = (m, tab) => tab === 'home' || tab === 'all' || MX.tabsOf(m).includes(tab);

// Badges on a poster: type (top-left, only for non-movies) + language (top-right)
const mxBadges = (m) => {
    const ty = MX.typeOf(m), lang = MX.langBadge(m);
    return (ty !== 'Movie' ? `<span class="nf-badge">${esc(ty.toUpperCase())}</span>` : '') + (lang ? `<span class="nf-lang">${esc(lang)}</span>` : '');
};
// Meta line: type icon | star rating | year | industry | first genre
const mxMeta = (m, withGenre = true) => {
    const r = MX.rating(m), g = MX.genresOf(m)[0];
    const parts = [`<span><i class="fas ${m.contentType === 'series' ? 'fa-tv' : 'fa-film'} mx-ic"></i></span>`];
    if (r) parts.push(`<span class="mx-rt"><i class="fas fa-star"></i> ${esc(r)}</span>`);
    if (m.year) parts.push(`<span>${esc(m.year)}</span>`);
    if (m.industry) parts.push(`<span>${esc(m.industry)}</span>`);
    if (withGenre && g) parts.push(`<span>${esc(g)}</span>`);
    return `<div class="mx-meta">${parts.join('')}</div>`;
};
// "Hot" score: 50% admin rating, 30% real plays, 20% My List adds (falls back to rating when there is no data yet)
const mxHot = (id, m) => {
    const s = mxStats[id] || {}, vals = Object.values(mxStats);
    const maxP = Math.max(1, ...vals.map(x => x.plays || 0)), maxL = Math.max(1, ...vals.map(x => x.lists || 0));
    return (MX.ratingNum(m) / 10) * 0.5 + ((s.plays || 0) / maxP) * 0.3 + ((s.lists || 0) / maxL) * 0.2;
};
const mxSortHot = (arr) => [...arr].sort((a, b) => mxHot(b[0], b[1]) - mxHot(a[0], a[1]) || mxWhen(b[1]) - mxWhen(a[1]));
// vertical grid card: poster + one-line title (MovieBox style)
const mxGridCard = (id, m) => `
    <div class="mx-gcard" onclick="showMovieDetails('${id}')">
        <div class="mx-gp" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}</div>
        <p class="mx-gt">${esc(m.title)}</p>
    </div>`;

// Long lists are drawn in small batches (infinite scroll): the first screen appears instantly, more cards are added as the person scrolls.
window.mxChunk = (box, items, render, size = 24) => {
    if (box._mxIo) { box._mxIo.disconnect(); box._mxIo = null; }
    box.innerHTML = '';
    const sentinel = document.createElement('div');
    sentinel.className = 'mx-sentinel'; sentinel.style.cssText = 'grid-column:1/-1;height:1px';
    box.appendChild(sentinel);
    let at = 0, io = null;
    const next = () => {
        const part = items.slice(at, at + size); at += part.length;
        sentinel.insertAdjacentHTML('beforebegin', part.map(render).join(''));
        if (at >= items.length) { if (io) io.disconnect(); box._mxIo = null; sentinel.remove(); }
        else if (io) { io.unobserve(sentinel); io.observe(sentinel); }   // still on screen? fire again
    };
    if (typeof IntersectionObserver === 'undefined') { sentinel.remove(); box.innerHTML = items.map(render).join(''); return; }
    io = new IntersectionObserver((es) => { if (es.some(e => e.isIntersecting)) next(); }, { rootMargin: '800px' });
    box._mxIo = io;
    next();
    if (at < items.length) io.observe(sentinel);
};
