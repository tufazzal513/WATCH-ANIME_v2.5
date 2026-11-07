/**
 * MoveX User - bootstrap
 * 1) Injects every <div data-include="..."> (partials + pages) into the DOM.
 * 2) Loads the app scripts one by one, in the original order.
 * The zero-delay scripts (theme-boot.js, splash-boot.js) stay synchronous in index.html.
 * NOTE: needs a web server (Firebase Hosting, `python3 -m http.server`, ...);
 *       fetch() does not work when the file is opened with file://
 */
(function () {
  var here = document.currentScript.src;                    // .../assets/js/user/bootstrap.js
  var SITE_ROOT = new URL('../../../', here).href;          // site root
  var JS_ROOT = new URL('./', here).href;                   // .../assets/js/user/

  var SCRIPTS = [
      "../shared/deploy-config.js",
      "../shared/config.js",
      "../shared/firebase-config.js",
      "../shared/api-client.js",
      "../shared/taxonomy.js",
      "core/firebase-init.js",
      "core/app-details.js",
      "core/state.js",
      "core/ui-kit.js",
      "core/utils.js",
      "core/error-logger.js",
      "core/carousel-state.js",
      "core/presence.js",
      "core/user-record.js",
      "features/auth-forms.js",
      "core/auth.js",
      "core/globals.js",
      "features/user-lists.js",
      "features/broken-link-reporter.js",
      "pages/home-carousel.js",
      "pages/home.js",
      "pages/notifications.js",
      "pages/search.js",
      "pages/categories.js",
      "pages/category-movies.js",
      "core/modal.js",
      "pages/watch-player.js",
      "core/theme-engine.js",
      "core/user-data.js"
  ];

  function fail(err) {
    console.error(err);
    var loader = document.getElementById('initial-loader');
    if (loader) {
      loader.textContent = 'Failed to start: ' + err.message + ' (open it through a web server, not file://)';
      loader.style.color = '#f87171'; loader.style.padding = '24px'; loader.style.textAlign = 'center';
    }
  }

  function loadIncludes() {
    var nodes = Array.prototype.slice.call(document.querySelectorAll('[data-include]'));
    return Promise.all(nodes.map(function (n) {
      return fetch(SITE_ROOT + n.getAttribute('data-include')).then(function (r) {
        if (!r.ok) throw new Error(r.status + ' ' + n.getAttribute('data-include'));
        return r.text();
      });
    })).then(function (parts) {
      nodes.forEach(function (n, i) { n.outerHTML = parts[i]; });
    });
  }

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement('script');
      s.src = src; s.onload = resolve;
      s.onerror = function () { reject(new Error('Cannot load ' + src)); };
      document.body.appendChild(s);
    });
  }

  loadIncludes().then(function () {
    return SCRIPTS.reduce(function (p, f) { return p.then(function () { return loadScript(JS_ROOT + f); }); }, Promise.resolve());
  }).catch(fail);
})();
