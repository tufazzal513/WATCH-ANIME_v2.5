// Broken-link reports are validated, de-duplicated, and rate-limited by the Go API.
window.reportBrokenLink = function (serverType, url) {
    if (!currentMovieId) return;
    const movie = allMovies[currentMovieId];
    if (!movie) return;
    const episode = currentSeasonKey && movie.seasons && movie.seasons[currentSeasonKey]
        ? movie.seasons[currentSeasonKey][currentEpIndex] : null;
    const epInfo = episode ? currentSeasonKey + ' - ' + (episode.name || ('Episode ' + (currentEpIndex + 1))) : 'Movie';
    MX_API.brokenLink({ movieId: currentMovieId, epInfo: epInfo, serverType: String(serverType || '').slice(0, 80), url: String(url || '').slice(0, 2048) })
        .catch(err => console.warn('Could not submit broken-link report:', err));
};
