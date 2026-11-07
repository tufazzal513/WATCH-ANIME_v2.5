// User error reports are write-only and rate-limited by the Go API.
(function () {
    let lastLog = 0;
    function logError(payload) {
        const now = Date.now();
        if (now - lastLog < 2000 || !auth.currentUser) return;
        lastLog = now;
        MX_API.reportError(payload).catch(() => {});
    }
    window.addEventListener('error', e => logError({
        message: e.message || 'Script error', source: e.filename || '',
        line: e.lineno || 0, column: e.colno || 0
    }));
    window.addEventListener('unhandledrejection', e => logError({
        message: (e.reason && (e.reason.message || String(e.reason))) || 'Unhandled promise rejection'
    }));
})();
