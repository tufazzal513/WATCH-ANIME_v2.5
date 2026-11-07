/* MoveX admin: the only first-party script. No inline handlers anywhere (the CSP forbids them). */
(function () {
    'use strict';
    var meta = document.querySelector('meta[name="csrf-token"]');
    var token = meta ? meta.getAttribute('content') : '';

    // every htmx request carries the CSRF token
    document.body.addEventListener('htmx:configRequest', function (e) {
        if (token) { e.detail.headers['X-CSRF-Token'] = token; }
    });

    // a failed htmx request shows a message instead of failing silently
    document.body.addEventListener('htmx:responseError', function (e) {
        var xhr = e.detail.xhr;
        if (xhr && xhr.status === 401) { window.location.href = '/admin/login'; return; }
        window.alert('Request failed (' + (xhr ? xhr.status : 'network') + '). Please try again.');
    });
    document.body.addEventListener('htmx:sendError', function () {
        window.alert('Network error. Please check your connection and try again.');
    });

    function toggleSidebar() {
        var sb = document.getElementById('admin-sidebar');
        var ov = document.getElementById('sidebar-overlay');
        if (sb) { sb.classList.toggle('active'); }
        if (ov) { ov.classList.toggle('active'); }
    }

    document.addEventListener('click', function (e) {
        var t = e.target.closest('[data-action]');
        if (t && t.getAttribute('data-action') === 'toggle-sidebar') {
            e.preventDefault();
            toggleSidebar();
        }
    });

    // data-confirm="Are you sure?" on a form, button or link asks before it goes ahead
    document.addEventListener('submit', function (e) {
        var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
        if (msg && !window.confirm(msg)) { e.preventDefault(); }
    }, true);
    document.addEventListener('click', function (e) {
        var el = e.target.closest('button[data-confirm],a[data-confirm]');
        if (el && !window.confirm(el.getAttribute('data-confirm'))) {
            e.preventDefault();
            e.stopImmediatePropagation();
        }
    }, true);

    // All Movies page: select every row currently visible on this page.
    document.addEventListener('change', function (e) {
        if (e.target && e.target.matches('[data-select-all]')) {
            document.querySelectorAll('[data-row-select]').forEach(function (box) { box.checked = e.target.checked; });
        }
        if (e.target && e.target.matches('[data-row-select]')) {
            var all = Array.prototype.slice.call(document.querySelectorAll('[data-row-select]'));
            var master = document.querySelector('[data-select-all]');
            if (master) master.checked = all.length > 0 && all.every(function (box) { return box.checked; });
        }
    });

    // Copy one-time worker secrets only after an explicit click.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-copy-text]');
        if (!btn) return;
        var value = btn.getAttribute('data-copy-text') || '';
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(value).then(function () { btn.textContent = 'Copied'; }, function () { window.prompt('Copy this worker key:', value); });
        } else { window.prompt('Copy this worker key:', value); }
    });

    // flash messages fade out after a few seconds
    var flash = document.querySelector('[data-flash]');
    if (flash) { setTimeout(function () { flash.classList.add('hidden'); }, 6000); }
})();

// Movie form enhancements: previews and safe, text-only link detection.
(function () {
    'use strict';
    function setupMovieForm() {
        var form = document.querySelector('[data-movie-form]');
        if (!form) return;
        var upcoming = form.querySelector('[data-upcoming-toggle]');
        var releaseWrap = form.querySelector('[data-release-wrap]');
        var releaseDate = form.querySelector('[data-release-date]');
        var releaseTS = form.querySelector('[data-release-ts]');
        function syncUpcoming() {
            var enabled = !!(upcoming && upcoming.checked);
            if (releaseWrap) releaseWrap.classList.toggle('hidden', !enabled);
            if (releaseDate) { releaseDate.required = enabled; if (!enabled) releaseDate.value = ''; }
        }
        if (upcoming) { upcoming.addEventListener('change', syncUpcoming); syncUpcoming(); }
        form.querySelectorAll('[data-image-url]').forEach(function (input) {
            var kind = input.getAttribute('data-image-url');
            var preview = form.querySelector('[data-image-preview="' + kind + '"]');
            var empty = form.querySelector('[data-image-empty="' + kind + '"]');
            function updatePreview() {
                var value = input.value.trim();
                if (!preview) return;
                if (!value) { preview.removeAttribute('src'); preview.classList.add('hidden'); if (empty) empty.hidden = false; return; }
                preview.src = value;
                preview.classList.remove('hidden');
                if (empty) empty.hidden = true;
            }
            input.addEventListener('input', updatePreview);
            input.addEventListener('change', updatePreview);
            if (preview) preview.addEventListener('error', function () { preview.classList.add('hidden'); if (empty) { empty.hidden = false; empty.textContent = 'Image preview unavailable'; } });
        });
        var video = form.querySelector('[data-video-url]');
        var kind = form.querySelector('[data-link-kind]');
        function detectLink() {
            if (!video || !kind) return;
            var value = video.value.trim().toLowerCase();
            var label = 'Custom URL';
            if (!value) label = 'Waiting for URL';
            else if (value.indexOf('youtube.com') >= 0 || value.indexOf('youtu.be') >= 0) label = 'YouTube video';
            else if (value.indexOf('vimeo.com') >= 0) label = 'Vimeo video';
            else if (value.indexOf('videasy.net') >= 0) label = 'Videasy player';
            else if (value.indexOf('/embed') >= 0 || value.indexOf('drive.google.com') >= 0) label = 'Embed / iframe';
            else if (value.indexOf('dropbox.com') >= 0) label = 'Dropbox link';
            else if (/\.m3u8(?:[?#]|$)/i.test(value)) label = 'HLS stream';
            else if (/\.(mp4|mkv|webm)(?:[?#]|$)/i.test(value)) label = 'Direct video';
            kind.textContent = label;
        }
        if (video) { video.addEventListener('input', detectLink); detectLink(); }
        form.addEventListener('submit', function () {
            if (upcoming && upcoming.checked && releaseDate && releaseTS) {
                var parsed = Date.parse(releaseDate.value);
                releaseTS.value = Number.isFinite(parsed) ? String(parsed) : '';
            }
        });
    }
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', setupMovieForm);
    else setupMovieForm();
})();
