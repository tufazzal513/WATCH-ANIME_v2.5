// Tailwind (v3) scans the admin templates and admin.js. Run ./build-css.sh after changing either.
module.exports = {
  content: ['./templates/**/*.html', './static/js/admin.js'],
  theme: { extend: {} },
  plugins: [],
};
