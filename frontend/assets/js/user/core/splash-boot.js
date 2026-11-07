(function() {
    const appName = localStorage.getItem('movex_app_name') || 'MoveX';
    const appLogo = localStorage.getItem('movex_app_logo');

    document.getElementById('splash-title-text').innerText = appName;
    document.getElementById('splash-footer-text').innerText = appName;

    if (appLogo) {
        document.getElementById('splash-logo-img').src = appLogo;
        document.getElementById('splash-logo-img').style.display = 'block';
        document.getElementById('splash-logo-box').style.background = 'transparent';
        document.getElementById('splash-logo-box').style.boxShadow = 'none';
    } else {
        document.getElementById('splash-logo-text').innerText = appName.substring(0, 2).toUpperCase();
        document.getElementById('splash-logo-text').style.display = 'block';
    }
})();

// Show the intro only once per browser session; a refresh keeps a quiet dark screen (no flash)
(function () {
    const loader = document.getElementById('initial-loader');
    if (sessionStorage.getItem('movex_splash_seen')) { if (loader) loader.classList.add('quiet'); }
    else sessionStorage.setItem('movex_splash_seen', '1');
    const m = /[#&]movie=([A-Za-z0-9_\-]+)/.exec(location.hash || '');
    if (m) sessionStorage.setItem('pending_movie', m[1]);
})();
