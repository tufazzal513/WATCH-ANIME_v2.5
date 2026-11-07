// Verifies Firebase Auth ID tokens (RS256 JWTs) with Node's built-in crypto. No npm packages.
import crypto from 'node:crypto';

const JWKS_URL = 'https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com';

export function createVerifier({ projectId, devInsecure = false, fetchKeys }) {
  let keys = new Map();
  let keysExp = 0;
  let lastForced = 0;
  const cache = new Map(); // token -> { auth, exp }

  const defaultFetch = async () => {
    const r = await fetch(JWKS_URL);
    if (!r.ok) throw new Error('jwks_fetch_failed');
    const m = /max-age=(\d+)/.exec(r.headers.get('cache-control') || '');
    return { jwk: await r.json(), ttl: m ? Number(m[1]) * 1000 : 3600e3 };
  };
  const getKeys = fetchKeys || defaultFetch;

  async function loadKeys(force) {
    if (!force && keys.size && Date.now() < keysExp) return;
    if (force && Date.now() - lastForced < 60e3) return;   // unknown kid: refetch at most once a minute
    if (force) lastForced = Date.now();
    const { jwk, ttl } = await getKeys();
    keys = new Map(jwk.keys.map(k => [k.kid, crypto.createPublicKey({ key: k, format: 'jwk' })]));
    keysExp = Date.now() + (ttl || 3600e3);
  }

  const fail = () => { const e = new Error('invalid_token'); e.status = 401; throw e; };

  async function verify(token) {
    const hit = cache.get(token);
    if (hit && hit.exp > Date.now()) return hit.auth;

    if (devInsecure && token.startsWith('dev:')) {          // TESTS ONLY - never enable in production
      return { uid: token.slice(4), email: token.slice(4) + '@dev.local' };
    }

    const parts = token.split('.');
    if (parts.length !== 3) fail();
    let header, payload;
    try {
      header = JSON.parse(Buffer.from(parts[0], 'base64url').toString());
      payload = JSON.parse(Buffer.from(parts[1], 'base64url').toString());
    } catch { fail(); }
    if (header.alg !== 'RS256' || !header.kid) fail();

    try { await loadKeys(false); } catch { fail(); }
    let key = keys.get(header.kid);
    if (!key) { try { await loadKeys(true); } catch { fail(); } key = keys.get(header.kid); }
    if (!key) fail();

    const ok = crypto.verify('RSA-SHA256', Buffer.from(parts[0] + '.' + parts[1]), key, Buffer.from(parts[2], 'base64url'));
    if (!ok) fail();

    const now = Math.floor(Date.now() / 1000);
    if (!payload.exp || payload.exp <= now) fail();
    if (!payload.iat || payload.iat > now + 300) fail();
    if (payload.aud !== projectId) fail();
    if (payload.iss !== `https://securetoken.google.com/${projectId}`) fail();
    if (typeof payload.sub !== 'string' || !payload.sub || payload.sub.length > 128) fail();

    const auth = { uid: payload.sub, email: payload.email || '' };
    cache.set(token, { auth, exp: Math.min(payload.exp * 1000, Date.now() + 10 * 60e3) });
    if (cache.size > 5000) { const n = Date.now(); for (const [k, v] of cache) if (v.exp < n) cache.delete(k); if (cache.size > 5000) cache.clear(); }
    return auth;
  }
  return { verify };
}
