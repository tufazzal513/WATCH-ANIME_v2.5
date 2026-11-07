// --- DASHBOARD (counts + analytics bind) ---
var __dashboardBound = false;
function updateDashboard(){
    if(__dashboardBound) return;
    __dashboardBound = true;

    // Genres (fixed list)
    (function(){ const el = document.getElementById('categories-count'); if(el && window.MX) el.textContent = MX.GENRES.length; })();

    // Banners Count Update
    db.ref('banners').on('value', s => {
        const el = document.getElementById('banners-count');
        if(el) el.textContent = s.numChildren();
    });

    db.ref('users').on('value', s => {
        const users = s.val() || {};

        // Update User Growth Chart
        cachedUsersForChart = users;
        renderUserGrowthChart();

        let actualUserCount = 0;
        let active24hCount = 0;
        const now = Date.now();
        const ONE_DAY_MS = 24 * 60 * 60 * 1000; // ২৪ ঘণ্টার মিলিসেকেন্ড

        Object.values(users).forEach(user => {
            if (user && user.status !== 'deleted') {
                actualUserCount++; // মোট রিয়েল ইউজার কাউন্ট

                // গত ২৪ ঘণ্টায় কতজন ইউজার অনলাইনে এসেছে তার কাউন্ট
                const lastSeen = user.last_seen || user.lastLoginAt || 0;
                if (now - lastSeen <= ONE_DAY_MS) {
                    active24hCount++;
                }
            }
        });

        // Total Users কার্ডে মোট ইউজার দেখাবে
        const elTotal = document.getElementById('total-users-count');
        if(elTotal) elTotal.textContent = actualUserCount;

        // Active (24h) কার্ডে গত ২৪ ঘণ্টার ইউজার দেখাবে
        const el24h = document.getElementById('users-count');
        if(el24h) el24h.textContent = active24hCount;
    });

    db.ref('online_users').on('value', s=>{
        const data = s.val() || {};
        const now = Date.now();
        const LIVE_WINDOW_MS = 90*1000;
        const live = Object.values(data).filter(u => u && u.last_seen && (now - u.last_seen) <= LIVE_WINDOW_MS).length;
        const el = document.getElementById('live-users-count');
        if(el) el.textContent = live;
    });

    // Password Resets Analytics
    db.ref('pwd_resets').on('value', s => {
        const data = s.val() || {};
        let totalResets = 0;
        let resets24h = 0;
        const now = Date.now();
        const ONE_DAY_MS = 24 * 60 * 60 * 1000;

        Object.values(data).forEach(userResets => {
            if(userResets) {
                Object.values(userResets).forEach(timestamp => {
                    totalResets++; // Count total
                    if(now - timestamp <= ONE_DAY_MS) {
                        resets24h++; // Count last 24h
                    }
                });
            }
        });

        const elTotalResets = document.getElementById('pwd-reset-total');
        if(elTotalResets) elTotalResets.textContent = totalResets;

        const el24hResets = document.getElementById('pwd-reset-24h');
        if(el24hResets) el24hResets.textContent = resets24h;
    });

    db.ref('movies').lite().on('value', s=>{
        const data = s.val() || {};
        const all = Object.keys(data).length;

        const now = Date.now();
        let series = 0;
        let upcoming = 0;
        let private = 0;

        Object.entries(data).forEach(([id,m])=>{
            if(!m) return;
            const type = (m.contentType || m.type || 'movie');
            if(type === 'series') series += 1;
            if(m.isPrivate) private += 1;

            const ts = (typeof m.releaseTs === 'number') ? m.releaseTs : (m.releaseDate ? Date.parse(m.releaseDate) : 0);
            const isUpcoming = !!m.isUpcoming && ts && ts > now;
            if(isUpcoming) upcoming += 1;

            if(m.isUpcoming && ts && ts <= now){
                db.ref(`movies/${id}/isUpcoming`).set(false);
            }
        });

        const moviesEl = document.getElementById('movies-count');
        if(moviesEl) moviesEl.textContent = all;

        const seriesEl = document.getElementById('series-count');
        if(seriesEl) seriesEl.textContent = series;

        // Total Movies (মোট কনটেন্ট থেকে সিরিজ বাদ দিলে মুভি পাওয়া যাবে)
        const singleMoviesEl = document.getElementById('single-movies-count');
        if(singleMoviesEl) singleMoviesEl.textContent = (all - series);

        const upEl = document.getElementById('upcoming-count');
        if(upEl) upEl.textContent = upcoming;

        const privateEl = document.getElementById('private-count');
        if(privateEl) privateEl.textContent = private;
    });}
