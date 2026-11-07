#!/usr/bin/env bash
# Rebuilds static/css/tailwind.css from the templates (Tailwind CSS 3.4.17, needs Node/npm and network once).
# Run it after ANY change to templates/*.html or static/js/admin.js; a Go test fails when you forget.
set -euo pipefail
cd "$(dirname "$0")"
npx --yes tailwindcss@3.4.17 -c tailwind.config.js -i tailwind.input.css -o static/css/tailwind.css --minify
LC_ALL=C cat templates/*.html static/js/admin.js | sha256sum | cut -d' ' -f1 > static/css/tailwind.css.sources
echo "tailwind.css rebuilt ($(wc -c < static/css/tailwind.css) bytes)"
