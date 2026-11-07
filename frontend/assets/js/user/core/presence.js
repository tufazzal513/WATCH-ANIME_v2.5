// Lightweight HTTPS presence. The server keeps online state in memory.
function setupPresence(user) {
    if (window.__presenceCleanup) window.__presenceCleanup();
    if (!user || !user.uid) return;
    let stopped = false;
    const beat = () => {
        if (stopped || document.visibilityState === 'hidden' || !auth.currentUser) return;
        MX_API.presence(user.displayName || 'User').catch(() => {});
    };
    const onVis = () => { if (document.visibilityState === 'visible') beat(); };
    const leave = () => { if (!stopped) MX_API.leavePresence().catch(() => {}); };
    beat();
    const timer = setInterval(beat, 60000);
    document.addEventListener('visibilitychange', onVis);
    window.addEventListener('pagehide', leave);
    window.__presenceCleanup = () => {
        if (stopped) return;
        stopped = true;
        clearInterval(timer);
        document.removeEventListener('visibilitychange', onVis);
        window.removeEventListener('pagehide', leave);
        MX_API.leavePresence().catch(() => {});
        window.__presenceCleanup = null;
    };
}
