/* ONE-TIME TOOLS for the admin console.
 *  - cleanupOldData()  : removes data of removed systems (comments, downloads, free/VIP server, ad settings, old categories list)
 *  - migrateGenres()   : turns the old single 'category' text (e.g. "Anime-Action") into the new genres list
 * (Both are explained below; each one is a dry run unless you pass true.)
 * HOW TO USE
 *  1. Deploy the new version first, open the ADMIN PANEL and log in as admin.
 *  2. Open the browser console (F12 -> Console), paste this whole file, press Enter.
 *  3. Run  cleanupOldData()        -> only SHOWS what would be deleted (dry run)
 *  4. Run  cleanupOldData(true)    -> really deletes it. This cannot be undone.
 * Movies that only had a VIP/second link keep it: it is moved to the single Video Link first.
 */
window.cleanupOldData = async function (really) {
    const updates = {};
    const log = [];
    const nodes = ['comments', 'downloadLinks', 'analytics/downloadGate', 'categories'];
    const keys = ['dlGateEnabled', 'dlAdLimit', 'dlAdSeconds', 'dlAdUrl', 'commentModEnabled', 'commentDislikeThreshold', 'smartAdEnabled', 'adTimer', 'smartAdWatchSeconds', 'adUrl'];
    nodes.forEach(n => { updates[n] = null; log.push('delete node: ' + n); });
    keys.forEach(k => { updates['settings/' + k] = null; log.push('delete setting: settings/' + k); });

    const snap = await db.ref('movies').once('value');
    const movies = snap.val() || {};
    let moved = 0, nulled = 0;
    Object.entries(movies).forEach(([id, m]) => {
        if (m.server2 !== undefined) { if (!m.server1 && m.server2) { updates[`movies/${id}/server1`] = m.server2; moved++; } updates[`movies/${id}/server2`] = null; nulled++; }
        if (m.allowDownload !== undefined) { updates[`movies/${id}/allowDownload`] = null; nulled++; }
        Object.entries(m.seasons || {}).forEach(([sk, eps]) => {
            (eps || []).forEach((e, i) => {
                const base = `movies/${id}/seasons/${sk}/${i}`;
                if (e.server2 !== undefined) { if (!e.server1 && e.server2) { updates[base + '/server1'] = e.server2; moved++; } updates[base + '/server2'] = null; nulled++; }
                if (e.allowDownload !== undefined) { updates[base + '/allowDownload'] = null; nulled++; }
            });
        });
    });
    log.push(`movie/episode fields to remove: ${nulled}, second links moved to Video Link: ${moved}`);
    console.log(log.join('\n'));
    if (!really) { console.log('DRY RUN only. Run cleanupOldData(true) to delete for real.'); return; }
    await db.ref().update(updates);
    console.log('Done. Old data removed.');
};

/* migrateGenres(): for every title that has no genres list yet, read the old category text, find the real genres inside it
 * (the fixed A-Z list), save them as `genres`, keep the first one in `category`, and set Type = Anime when the old category said Anime.
 * Run  migrateGenres()  to preview,  migrateGenres(true)  to save. Titles that already have genres are not touched. */
window.migrateGenres = async function (really) {
    if (typeof MX === 'undefined') { console.log('Open the admin panel first (the genre list is not loaded here).'); return; }
    const movies = (await db.ref('movies').once('value')).val() || {};
    const updates = {}, rows = [];
    Object.entries(movies).forEach(([id, m]) => {
        if (Array.isArray(m.genres) && m.genres.length) return;
        const g = MX.genresOf(m), upd = {};
        if (g.length) { upd.genres = g; if (m.category !== g[0]) upd.category = g[0]; }
        if (!m.type && /anime/i.test(String(m.category || ''))) upd.type = 'Anime';
        if (!Object.keys(upd).length) return;
        Object.entries(upd).forEach(([k, v]) => { updates['movies/' + id + '/' + k] = v; });
        rows.push((m.title || id) + ':  "' + (m.category || '') + '"  ->  genres [' + (upd.genres || []).join(', ') + ']' + (upd.type ? '  + Type Anime' : ''));
    });
    console.log(rows.join('\n') || 'Nothing to migrate.');
    console.log(rows.length + ' title(s) will change.');
    if (!really) { console.log('DRY RUN only. Run migrateGenres(true) to save.'); return; }
    if (rows.length) await db.ref().update(updates);
    console.log('Done.');
};
console.log('Loaded. Run cleanupOldData() or migrateGenres() first (both are dry runs).');
