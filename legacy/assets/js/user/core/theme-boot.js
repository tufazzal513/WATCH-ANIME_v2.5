// TRUE ZERO-DELAY: Blocks DOM until Name and Theme are resolved
const cachedAppName = localStorage.getItem('movex_app_name') || 'MoveX';
const cTheme1 = localStorage.getItem('movex_theme_color') || '#e50914';
const cTheme2 = localStorage.getItem('movex_theme_color2') || '#b20710';
document.title = cachedAppName;

// Inject theme instantly before page renders to stop color flickering
document.write(`
    <style>
        :root { --accent: ${cTheme1}; --accent2: ${cTheme2}; --primary: var(--accent); }
    </style>
`);
