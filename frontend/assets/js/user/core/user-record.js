// The Go API creates the user record and owns account status.
async function ensureUserRecord(user) {
    if (!user || !user.uid) return null;
    try {
        const me = await MX_API.me();
        return me && me.status ? me.status : 'active';
    } catch (error) {
        if (error && error.status === 403 && error.body && error.body.status) return error.body.status;
        throw error;
    }
}


test('auth state transitions ignore stale account initialization and status responses', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/core/auth.js'), 'utf8');
  assert.match(source, /let authTransitionID = 0/);
  assert.match(source, /function isCurrentAuthUser\(user, transitionID\)/);
  assert.match(source, /const transitionID = \+\+authTransitionID/);
  assert.match(source, /if \(!isCurrentAuthUser\(user, transitionID\)\) return;/);
  assert.match(source, /transitionID === authTransitionID && document\.visibilityState === 'visible'/);
  assert.match(source, /window\.stopUserDataRefresh\(\)/);
  assert.match(source, /localStorage\.removeItem\('last_movie_id'\)/);
});

test('catalog refreshes cannot let a previous account overwrite the current session', () => {
  const source = fs.readFileSync(path.join(root, 'assets/js/user/core/user-data.js'), 'utf8');
  assert.match(source, /function currentUID\(\)/);
  assert.match(source, /const requestID = \+\+catalogRequestID/);
  assert.match(source, /requestID !== catalogRequestID \|\| currentUID\(\) !== uid/);
  assert.match(source, /window\.stopUserDataRefresh = function \(\)/);
  assert.match(source, /catalogRequestID\+\+/);
  assert.match(source, /allMovies = \{\}/);
});
