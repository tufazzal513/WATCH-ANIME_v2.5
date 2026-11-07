// Error logging (unchanged)
(function(){
    let lastLog = 0;
    function logError(payload){
        const now = Date.now();
        if(now - lastLog < 2000) return;
        lastLog = now;
        try{
            db.ref('analytics/errors').push(Object.assign({ts: now, panel: 'admin'}, payload));
        }catch(e){}
    }
    window.addEventListener('error', (e)=>{
        logError({
            message: e.message || 'Script error',
            source: e.filename || '',
            lineno: e.lineno || 0,
            colno: e.colno || 0
        });
    });
    window.addEventListener('unhandledrejection', (e)=>{
        logError({ message: (e.reason && (e.reason.message || String(e.reason))) || 'Unhandled promise rejection' });
    });
})();

// Multiple Admin System - UID checked directly from Database
