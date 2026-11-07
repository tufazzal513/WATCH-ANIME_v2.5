// --- GLOBAL SETTINGS & DYNAMIC THEME ENGINE (Runs instantly for everyone) ---
function shadeColor(color, percent) {
    let R = parseInt(color.substring(1,3),16);
    let G = parseInt(color.substring(3,5),16);
    let B = parseInt(color.substring(5,7),16);
    R = parseInt(R * (100 + percent) / 100);
    G = parseInt(G * (100 + percent) / 100);
    B = parseInt(B * (100 + percent) / 100);
    R = (R<255)?R:255; G = (G<255)?G:255; B = (B<255)?B:255;
    let RR = ((R.toString(16).length==1)?"0"+R.toString(16):R.toString(16));
    let GG = ((G.toString(16).length==1)?"0"+G.toString(16):G.toString(16));
    let BB = ((B.toString(16).length==1)?"0"+B.toString(16):B.toString(16));
    return "#"+RR+GG+BB;
}

let themeAutoInterval;

db.ref('settings').on('value', s => {
    const d = s.val() || {};

    // Ads & Others Configuration
    pwdResetLimit = parseInt(d.pwdResetLimit ?? 3, 10) || 3;

    // --- PREMIUM DYNAMIC THEME ENGINE ---
    const mode = d.themeMode || 'static';
    const staticColor = d.themeColor || '#e50914';
    const intervalMinutes = parseInt(d.themeInterval) || 60;

    const applyTheme = (hex) => {
        const darkHex = shadeColor(hex, -20); 

        // Save to Cache for Zero-Delay Reloads
        localStorage.setItem('movex_theme_color', hex);
        localStorage.setItem('movex_theme_color2', darkHex);

        // INSTANT GLOBAL CSS VARIABLES UPDATE
        document.documentElement.style.setProperty('--accent', hex);
        document.documentElement.style.setProperty('--accent2', darkHex);
        document.documentElement.style.setProperty('--primary', hex);
    };

    clearInterval(themeAutoInterval);

    if (mode === 'static') {
        applyTheme(staticColor);
    } else if (mode === 'auto') {
        const premiumColors =['#e50914', '#2563eb', '#9333ea', '#059669', '#ea580c', '#ec4899', '#0891b2'];
        const calculateAutoColor = () => {
            const currentCycle = Math.floor(Date.now() / (intervalMinutes * 60 * 1000));
            const colorIndex = currentCycle % premiumColors.length;
            applyTheme(premiumColors[colorIndex]);
        };
        calculateAutoColor();
        themeAutoInterval = setInterval(calculateAutoColor, 30000);
    }
});
