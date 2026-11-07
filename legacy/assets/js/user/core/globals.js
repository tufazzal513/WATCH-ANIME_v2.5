// --- GLOBAL VARS ---
let playerMode = 'direct';
let isPlaying = false;
let vimeoPlayer, ytPlayer, updateInterval;
let searchHistory = JSON.parse(localStorage.getItem('movex_search_history')) || [];

// keep the desktop top navigation clear of the app name (it starts right after the name, whatever its length)
(function () {
    const w = document.querySelector('#global-header .nf-wordmark');
    if (!w) return;
    const fit = () => { const r = w.getBoundingClientRect(); if (r.right) document.documentElement.style.setProperty('--nav-left', (Math.ceil(r.right) + 28) + 'px'); };
    if (window.ResizeObserver) new ResizeObserver(fit).observe(w);
    window.addEventListener('resize', fit); fit();
})();
