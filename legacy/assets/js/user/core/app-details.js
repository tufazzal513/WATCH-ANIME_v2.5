// --- INSTANT LOAD APP DETAILS (LOGO & NAME) ---
function applyAppDetails(name, logoUrl) {
    if(name) {
        localStorage.setItem('movex_app_name', name);
        document.title = name;
        document.querySelectorAll('.dynamic-app-name').forEach(el => {
            el.innerHTML = name.replace(/(.{1,2})$/, '<span class="theme-text-color">$1</span>');
        });
        const profileNameEl = document.getElementById('profile-name');
        if (profileNameEl && (!auth.currentUser || !auth.currentUser.displayName)) {
            profileNameEl.textContent = `${name} User`;
        }
        const logoText = document.getElementById('splash-logo-text');
        if(logoText) logoText.textContent = name.substring(0, 2).toUpperCase();
    }

    if(logoUrl !== undefined) {
        const logoImg = document.getElementById('splash-logo-img');
        const logoText = document.getElementById('splash-logo-text');
        const logoBox = document.getElementById('splash-logo-box');

        const headerLogoImg = document.getElementById('header-logo-img');

        if(logoUrl) {
            localStorage.setItem('movex_app_logo', logoUrl);
            if(logoImg) { logoImg.src = logoUrl; logoImg.style.display = 'block'; }
            if(logoText) logoText.style.display = 'none';
            if(logoBox) {
                logoBox.style.background = 'transparent';
                /* Permanent glow behind the logo matching the admin theme */
                logoBox.style.boxShadow = '0 0 50px var(--accent)';
            }
            if(headerLogoImg) {
                headerLogoImg.src = logoUrl;
                headerLogoImg.classList.remove('hidden');
            }
        } else {
            localStorage.removeItem('movex_app_logo');
            if(logoImg) logoImg.style.display = 'none';
            if(logoText) logoText.style.display = 'block';
            if(logoBox) {
                logoBox.style.background = 'linear-gradient(135deg, var(--accent), var(--accent2))';
                logoBox.style.boxShadow = '0 10px 30px rgba(0,0,0,0.5)';
            }
            if(headerLogoImg) {
                headerLogoImg.classList.add('hidden');
            }
        }
    }
}

// Apply cached data immediately for zero loading delay
applyAppDetails(localStorage.getItem('movex_app_name'), localStorage.getItem('movex_app_logo'));

// Listen for real-time updates from Firebase
db.ref('settings/appName').on('value', s => applyAppDetails(s.val() || "MoveX", undefined));
db.ref('settings/appLogo').on('value', s => applyAppDetails(undefined, s.val() || ""));
