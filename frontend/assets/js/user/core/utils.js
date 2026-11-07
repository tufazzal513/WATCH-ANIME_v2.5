// --- Utility Functions ---
function getReleaseMs(m){
    if(!m) return 0;
    if(typeof m.releaseTs === 'number') return m.releaseTs;
    if(m.releaseDate){
        const t = Date.parse(m.releaseDate);
        return isNaN(t) ? 0 : t;
    }
    return 0;
}
