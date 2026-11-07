db.ref('settings/appName').on('value', s => { const n = s.val()||"MoveX"; document.getElementById('app-name-input').value = n; document.querySelectorAll('.admin-app-name').forEach(el => el.innerHTML = n.replace(/^(.*?)(X)$/i, '$1<span class="text-blue-500">$2</span>') || n); });
db.ref('settings/appLogo').on('value', s => { document.getElementById('app-logo-input').value = s.val() || ""; });
document.getElementById('app-details-form').addEventListener('submit', e => { 
    e.preventDefault(); 
    db.ref('settings/appName').set(document.getElementById('app-name-input').value);
    db.ref('settings/appLogo').set(document.getElementById('app-logo-input').value);
    alert('App Details Updated!'); 
});

// Handle Theme Form Toggles
const themeRadios = document.querySelectorAll('input[name="theme-mode"]');
const staticBox = document.getElementById('theme-static-box');
const autoBox = document.getElementById('theme-auto-box');

themeRadios.forEach(radio => {
    radio.addEventListener('change', (e) => {
        if(e.target.value === 'static') {
            staticBox.classList.remove('hidden');
            autoBox.classList.add('hidden');
        } else {
            staticBox.classList.add('hidden');
            autoBox.classList.remove('hidden');
        }
    });
});

// Load Settings Data
db.ref('settings').on('value', s => {
    const data = s.val() || {};

    // Ads & Others
    document.getElementById('pwd-limit-input').value = data.pwdResetLimit || '3';

    // Theme Data
    const mode = data.themeMode || 'static';
    document.querySelector(`input[name="theme-mode"][value="${mode}"]`).checked = true;
    if(mode === 'auto') {
        staticBox.classList.add('hidden');
        autoBox.classList.remove('hidden');
    } else {
        staticBox.classList.remove('hidden');
        autoBox.classList.add('hidden');
    }
    document.getElementById('theme-color-picker').value = data.themeColor || '#e50914';
    document.getElementById('theme-interval-input').value = data.themeInterval || '60';
});

// Save Theme Data
document.getElementById('theme-settings-form').addEventListener('submit', e => {
    e.preventDefault();
    const mode = document.querySelector('input[name="theme-mode"]:checked').value;
    const color = document.getElementById('theme-color-picker').value;
    const interval = parseInt(document.getElementById('theme-interval-input').value) || 60;

    db.ref('settings').update({
        themeMode: mode,
        themeColor: color,
        themeInterval: interval
    }).then(() => alert('Theme settings updated successfully! Users will see changes instantly.'));
});

document.getElementById('pwd-reset-form').addEventListener('submit', e => {
    e.preventDefault();
    const limitVal = document.getElementById('pwd-limit-input').value;
    const parsed = parseInt(limitVal);
    if (!isNaN(parsed) && parsed > 0) {
        db.ref('settings').update({ pwdResetLimit: parsed }).then(() => {
            alert('Password Reset Limit updated successfully!');
        });
    } else {
        alert('Please enter a valid number greater than 0.');
    }
});

initEditableSocials();
initEditableNotifications();
