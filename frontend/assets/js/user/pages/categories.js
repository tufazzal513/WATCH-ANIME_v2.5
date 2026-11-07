// --- EXPLORE LIST (Genres / Industries / Languages / Types) ---
window.exploreTab = window.exploreTab || 'genre';
let exploreRequestID = 0;
let exploreFacets = null;
window.setExploreTab = (x) => { window.exploreTab = x; renderCategoriesPage(); };
const exploreFacetKey = { genre: 'categories', industry: 'industries', language: 'languages', type: 'types' };
const renderCategoriesPage = () => {
    const box = document.getElementById('categories-grid');
    if (!box) return;
    document.querySelectorAll('#ex-tabs button').forEach(b => b.classList.toggle('on', b.dataset.x === exploreTab));
    if (!exploreFacets) {
        const requestID = ++exploreRequestID;
        box.innerHTML = '<p class="nf-empty" role="status">Loading categories...</p>';
        MX_API.catalogFacets().then(data => {
            if (requestID !== exploreRequestID) return;
            exploreFacets = data || {};
            renderCategoriesPage();
        }).catch(err => {
            if (requestID !== exploreRequestID) return;
            console.error('MoveX Explore facets failed:', err);
            box.innerHTML = '<p class="nf-empty" role="status">Explore is temporarily unavailable. Please try again.</p>';
        });
        return;
    }
    const key = exploreFacetKey[exploreTab] || 'categories';
    const data = Array.isArray(exploreFacets[key]) ? exploreFacets[key] : [];
    const q = (s) => String(s).replace(/\\/g, '\\\\').replace(/'/g, "\\'");
    box.innerHTML = data.length
        ? data.map(item => `<button class="ex-item" onclick="openBrowse({${exploreTab}:'${q(item.name)}'})"><span>${esc(item.name)}</span><small>${Number(item.count) || 0}</small></button>`).join('')
        : '<p class="nf-empty">Nothing here yet.</p>';
};
