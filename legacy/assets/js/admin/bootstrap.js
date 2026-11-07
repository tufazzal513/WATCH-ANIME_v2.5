/**
 * MoveX Admin - bootstrap
 * 1) Injects every <div data-include="..."> (partials + pages) into the DOM.
 * 2) Loads the app scripts one by one, in the original order.
 * Result: the DOM and the code run exactly as in the old single-file panel.
 * NOTE: needs a web server (Firebase Hosting, `python3 -m http.server`, ...);
 *       fetch() does not work when the file is opened with file://
 */
(function () {
  var here = document.currentScript.src;                       // .../assets/js/admin/bootstrap.js
  var ADMIN_ROOT = new URL('../../../admin/', here).href;      // .../admin/
  var JS_ROOT = new URL('./', here).href;                      // .../assets/js/admin/

  var SCRIPTS = [
      "../shared/firebase-config.js",
      "../shared/db-client.js",
      "../shared/taxonomy.js",
      "core/firebase-init.js",
      "core/error-logger.js",
      "core/state.js",
      "core/auth.js",
      "core/navigation.js",
      "core/crud-helpers.js",
      "pages/seasons-episodes.js",
      "pages/socials-notifications.js",
      "pages/movie-form.js",
      "pages/broken-links.js",
      "pages/movie-actions.js",
      "pages/classification.js",
      "pages/banners.js",
      "pages/settings.js",
      "pages/server-backup.js",
      "pages/server-config.js",
      "pages/users.js",
      "pages/user-growth-chart.js",
      "pages/dashboard.js",
      "pages/link-detector.js",
      "core/init.js"
  ];

  function fail(err) {
    console.error(err);
    var loader = document.getElementById('global-loader');
    if (loader) {
      loader.textContent = 'Failed to start admin panel: ' + err.message +
        ' (open it through a web server, not file://)';
      loader.style.color = '#f87171';
      loader.style.padding = '24px';
      loader.style.textAlign = 'center';
    }
  }

  function loadIncludes() {
    var nodes = Array.prototype.slice.call(document.querySelectorAll('[data-include]'));
    return Promise.all(nodes.map(function (n) {
      return fetch(ADMIN_ROOT + n.getAttribute('data-include')).then(function (r) {
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
      s.src = src;
      s.onload = resolve;
      s.onerror = function () { reject(new Error('Cannot load ' + src)); };
      document.body.appendChild(s);
    });
  }

  loadIncludes()
    .then(function () {
      return SCRIPTS.reduce(function (p, f) {
        return p.then(function () { return loadScript(JS_ROOT + f); });
      }, Promise.resolve());
    })
    .catch(fail);
})();
