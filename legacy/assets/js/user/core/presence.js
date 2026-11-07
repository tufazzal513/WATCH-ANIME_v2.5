// --- LIVE USERS PRESENCE (light version) ---
// Old: 2 database writes every 30 s per user + a permanent connection.
// Now: one tiny request per minute, only while the tab is visible. The server keeps presence in memory
// (nothing is written to disk) and saves "last seen" to the user record at most every 10 minutes.
function setupPresence(user) {
    if (window.__presenceCleanup) window.__presenceCleanup();
    const onlineRef = db.ref('online_users/' + user.uid);
    const beat = () => {
        if (document.visibilityState === 'hidden') return;
        onlineRef.set({ name: user.displayName || "User", last_seen: Date.now() }).catch(() => {});
    };
    const onVis = () => { if (document.visibilityState === 'visible') beat(); };
    const onHide = () => { try { onlineRef.remove({ keepalive: true }); } catch (e) {} };

    beat();
    const timer = setInterval(beat, 60000);
    document.addEventListener('visibilitychange', onVis);
    window.addEventListener('pagehide', onHide);

    window.__presenceCleanup = () => {
        clearInterval(timer);
        document.removeEventListener('visibilitychange', onVis);
        window.removeEventListener('pagehide', onHide);
        window.__presenceCleanup = null;
    };
}
