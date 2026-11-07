// --- AUTOMATIC BROKEN LINK REPORTER ---
window.reportBrokenLink = function(serverType, url) {
    if(!currentMovieId) return;
    const movie = allMovies[currentMovieId];
    if(!movie) return;

    const epInfo = currentSeasonKey ? (currentSeasonKey + ' - ' + movie.seasons[currentSeasonKey][currentEpIndex].name) : 'Movie';

    // ডাটাবেজে অটো রিপোর্ট সেন্ড করা
    db.ref('broken_links/' + currentMovieId).set({
        movieId: currentMovieId,
        title: movie.title,
        type: movie.contentType || 'movie',
        posterUrl: movie.posterUrl,
        epInfo: epInfo,
        serverType: serverType,
        url: url,
        reportedAt: firebase.database.ServerValue.TIMESTAMP
    });
};
