/* MoveX runtime configuration.
 * Set window.MX_CONFIG before this file when injecting configuration at deploy time.
 * For GitHub Pages, apiBase must point to the HTTPS URL of the Go main server.
 */
(function (w) {
  'use strict';
  var supplied = w.MX_CONFIG && typeof w.MX_CONFIG === 'object' ? w.MX_CONFIG : {};
  var apiBase = typeof supplied.apiBase === 'string' ? supplied.apiBase.trim() : '';
  apiBase = apiBase.replace(/\/+$/, '');
  if (apiBase && !/^https:\/\//i.test(apiBase) && !/^http:\/\/localhost(?::\d+)?$/i.test(apiBase)) {
    throw new Error('MoveX configuration: apiBase must use HTTPS (HTTP is allowed only on localhost).');
  }
  var firebase = supplied.firebase && typeof supplied.firebase === 'object' ? supplied.firebase : {
    apiKey: 'AIzaSyBKBffOSZJgJcKastQEL71X7MlJlFhiPOo',
    authDomain: 'movex-513.firebaseapp.com',
    projectId: 'movex-513',
    storageBucket: 'movex-513.firebasestorage.app',
    messagingSenderId: '850803322721',
    appId: '1:850803322721:web:e4f7d47a2612647d0f5f20'
  };
  w.MX = Object.freeze({
    apiBase: apiBase,
    firebase: Object.freeze(firebase),
    pollMs: 60000,
    requestTimeoutMs: 20000,
    workerHealthTimeoutMs: 3500
  });
})(window);
