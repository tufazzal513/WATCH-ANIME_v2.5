document.getElementById('admin-search-input').addEventListener('input', (e) => {
    currentSearchTerm = e.target.value.toLowerCase();
    renderAllMoviesTable(allMoviesCache);
});

window.editMovie = (id) => {
    if (!allMoviesCache[id]) return;
    // the table list is "lite" (no episodes/links) -> load the full title before filling the form
    db.ref('movies/' + id).once('value').then(snap => {
        const full = snap.val();
        if (!full) return alert('This title no longer exists.');
        allMoviesCache[id] = full;
        window.__editMovieFull(id);
    }).catch(() => alert('Could not load this title. Try again.'));
};
window.__editMovieFull = (id) => {
    const movie = allMoviesCache[id];
    if (!movie) return;
    document.getElementById('movie-edit-id').value = id;
    document.getElementById('movie-title').value = movie.title || '';
    document.getElementById('movie-year').value = movie.year || '';
    document.getElementById('movie-poster').value = movie.posterUrl || '';
    document.getElementById('movie-rating').value = movie.rating || '';
    if (window.mxCls) mxCls.set(movie);
    document.getElementById('movie-cover').value = movie.coverUrl || '';
    document.getElementById('movie-cast').value = movie.cast || '';
    document.getElementById('movie-description').value = movie.description || '';
    document.getElementById('movie-is-private').checked = !!movie.isPrivate;

    const type = movie.contentType || 'movie';
    document.querySelector(`input[name="content-type"][value="${type}"]`).checked = true;
    toggleContentType();

    if (type === 'movie') {
        document.getElementById('movie-server1').value = movie.server1 || movie.server2 || '';
    } else {
        loadSeasonsFromMovie(movie);
    }

    if (movie.isUpcoming) {
        if (isUpcomingCheckbox) isUpcomingCheckbox.checked = true;
        if (releaseDateContainer) releaseDateContainer.classList.remove('hidden');
        const rdi = document.getElementById('movie-release-date');
        if (rdi) rdi.value = movie.releaseDate || '';
    } else {
        if (isUpcomingCheckbox) isUpcomingCheckbox.checked = false;
        if (releaseDateContainer) releaseDateContainer.classList.add('hidden');
    }

    movieCancelBtn.classList.remove('hidden');
    showAdminPage('movies');

    // এডিট মুভি লোড হওয়ার সাথে সাথে লিংকের ধরন শো করবে
    if(window.triggerLinkDetectors) window.triggerLinkDetectors();

    setTimeout(() => {
        document.getElementById('movie-form').scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, 100);
};

movieForm.addEventListener('submit', (e) => {
    e.preventDefault();
    const isUpcoming = document.getElementById('movie-is-upcoming').checked;
    const isPrivate = document.getElementById('movie-is-private').checked;
    const releaseVal = document.getElementById('movie-release-date').value;
    if(isUpcoming && !releaseVal){
        alert('Upcoming content must have a Release Date/Time.');
        return;
    }

    if (!mxCls.genres().length) { alert('Please select at least one genre.'); return; }

    const type = document.querySelector('input[name="content-type"]:checked').value;

    let formData = {
        title: document.getElementById('movie-title').value,
        year: document.getElementById('movie-year').value,
        posterUrl: document.getElementById('movie-poster').value,
        rating: document.getElementById('movie-rating').value,
        ...mxCls.get(),
        coverUrl: (document.getElementById('movie-cover').value || '').trim(),
        cast: document.getElementById('movie-cast').value,
        description: document.getElementById('movie-description').value,
        isPrivate: Boolean(isPrivate), // নিশ্চিতভাবে Boolean হিসেবে সেভ করা
        isUpcoming: Boolean(isUpcoming),
        releaseDate: isUpcoming ? document.getElementById('movie-release-date').value : null,
        releaseTs: isUpcoming ? (Date.parse(document.getElementById('movie-release-date').value) || null) : null,
        contentType: type
    };

    if (type === 'movie') {
        formData.server1 = document.getElementById('movie-server1').value.trim();
        formData.server2 = null;   // second (VIP) link removed
    } else {
        let seasonsObj = {};
        const emptySeasons = seasonsData.filter(x => !x.episodes.length);
        if (emptySeasons.length && !confirm('These seasons have no episodes and will NOT be saved (a season needs at least one episode):\n' + emptySeasons.map(x => x.name).join(', ') + '\n\nContinue?')) return;
        seasonsData.forEach((season, idx) => {
            if (!season.episodes.length) return;
            const seasonName = season.name || `Season ${idx+1}`;
            seasonsObj[seasonName] = season.episodes.map(ep => ({
                name: ep.name,
                server1: ep.server1 || ep.server2 || ''}));
        });
        formData.seasons = seasonsObj;
    }

    const editId = document.getElementById('movie-edit-id').value;
    if (editId) {
        db.ref(`movies/${editId}`).update(formData).then(() => {
            // Update successful, auto-remove any broken link reports for this movie
            db.ref(`broken_links/${editId}`).remove();

            resetMovieForm();
            alert('Content Updated Successfully!');
            showAdminPage('all-movies');
        });
    } else {
        db.ref('movies').push(formData).then(() => {
            resetMovieForm();
            alert('Content Added');
        });
    }
});


// live warning for http:// links (they are blocked on an https site)
document.addEventListener('input', (e) => {
    if (!e.target || !/^(movie-server1|edit-ep-server1)$/.test(e.target.id)) return;
    const warn = e.target.parentElement.querySelector('.http-warn');
    if (warn) warn.classList.toggle('hidden', !/^http:\/\//i.test(e.target.value.trim()));
});
