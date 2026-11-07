const movieForm = document.getElementById('movie-form');
const movieCancelBtn = document.getElementById('movie-form-cancel');
const isUpcomingCheckbox = document.getElementById('movie-is-upcoming');
const releaseDateContainer = document.getElementById('release-date-container');
const releaseDateInput = document.getElementById('movie-release-date');

if (isUpcomingCheckbox) {
    isUpcomingCheckbox.addEventListener('change', (e) => {
        const checked = !!e.target.checked;
        if (releaseDateContainer) releaseDateContainer.classList.toggle('hidden', !checked);
        if (releaseDateInput) {
            releaseDateInput.required = checked;
            if (!checked) releaseDateInput.value = '';
        }
    });
}

const resetMovieForm = () => {
    if (movieForm) movieForm.reset();
    if (window.mxCls) mxCls.reset();
    const editIdEl = document.getElementById('movie-edit-id');
    if (editIdEl) editIdEl.value = '';

    if (movieCancelBtn) movieCancelBtn.classList.add('hidden');
    if (releaseDateContainer) releaseDateContainer.classList.add('hidden');

    seasonsData = [];
    selectedSeasonIndex = 0;
    selectedEpisodeIndex = -1;
    renderSeasons();

    const movieType = document.querySelector('input[name="content-type"][value="movie"]');
    if (movieType) movieType.checked = true;

    if (typeof toggleContentType === 'function') toggleContentType();
};

movieCancelBtn?.addEventListener('click', resetMovieForm);

// Movies listener (unchanged)
db.ref('movies').lite().on('value', snapshot => {
    allMoviesCache = snapshot.val() || {};
    renderAllMoviesTable(allMoviesCache);
    document.getElementById('total-movies-badge').textContent = Object.keys(allMoviesCache).length;

    const catSelect = document.getElementById('category-filter');
    if(catSelect) {
        catSelect.innerHTML = '<option value="all">All Genres</option>' + MX.GENRES.map(c => `<option value="${c}">${c}</option>`).join('');
        const indSel = document.getElementById('industry-filter');
        if (indSel && indSel.options.length < 2) indSel.innerHTML = '<option value="all">All Industries</option>' + MX.INDUSTRIES.map(c => `<option value="${c}">${c}</option>`).join('');
        const lgSel = document.getElementById('lang-filter');
        if (lgSel && lgSel.options.length < 2) lgSel.innerHTML = '<option value="all">All Languages</option>' + MX.LANGUAGES.map(c => `<option value="${c}">${c}</option>`).join('');
    }

    const yearSelect = document.getElementById('year-filter');
    if(yearSelect) {
        const years = new Set();
        Object.values(allMoviesCache).forEach(m => {
            if(m.year) years.add(m.year);
        });
        yearSelect.innerHTML = '<option value="all">All Years</option>' + 
            Array.from(years).sort().reverse().map(y => `<option value="${y}">${y}</option>`).join('');
    }
});

// Render movies table (unchanged)
let currentTypeFilter = 'all';
let currentStatusFilter = 'all';
let currentUpcomingFilter = 'all';
let currentCategoryFilter = 'all';
let currentIndustryFilter = 'all';
let currentLangFilter = 'all';
let currentYearFilter = 'all';
let currentSearchTerm = '';

function renderAllMoviesTable(data) {
    const container = document.getElementById('all-movies-table');

    let filteredData = Object.entries(data);

    if(currentTypeFilter !== 'all') {
        filteredData = filteredData.filter(([_, m]) => (m.contentType || 'movie') === currentTypeFilter);
    }

    if(currentStatusFilter !== 'all') {
        const isPrivate = (currentStatusFilter === 'private');
        filteredData = filteredData.filter(([_, m]) => {
            // যদি মুভিতে isPrivate প্রপার্টি না থাকে, তবে সেটি 'public' হিসেবে ধরা হবে
            const movieStatus = !!m.isPrivate; 
            return movieStatus === isPrivate;
        });
    }

    if(currentUpcomingFilter !== 'all') {
        const now = Date.now();
        const wantUpcoming = currentUpcomingFilter === 'yes';
        filteredData = filteredData.filter(([_, m]) => {
            if(wantUpcoming) {
                const ts = m.releaseTs || (m.releaseDate ? Date.parse(m.releaseDate) : 0);
                return !!m.isUpcoming && ts > now;
            } else {
                const ts = m.releaseTs || (m.releaseDate ? Date.parse(m.releaseDate) : 0);
                return !m.isUpcoming || ts <= now;
            }
        });
    }

    if(currentCategoryFilter !== 'all') {
        filteredData = filteredData.filter(([_, m]) => MX.genresOf(m).includes(currentCategoryFilter));
    }
    if(currentIndustryFilter !== 'all') {
        filteredData = filteredData.filter(([_, m]) => m.industry === currentIndustryFilter);
    }
    if(currentLangFilter !== 'all') {
        filteredData = filteredData.filter(([_, m]) => MX.langsOf(m).includes(currentLangFilter));
    }

    if(currentYearFilter !== 'all') {
        filteredData = filteredData.filter(([_, m]) => String(m.year) === currentYearFilter);
    }

    if(currentSearchTerm) {
        filteredData = filteredData.filter(([_, m]) => m.title.toLowerCase().includes(currentSearchTerm));
    }

    const rows = filteredData.reverse(); 
    if(rows.length === 0) {
        container.innerHTML = '<tr><td colspan="7" class="p-4 text-center text-gray-500">No content found.</td></tr>';
        return;
    }
    container.innerHTML = rows.map(([id, movie]) => {
        let dateDisplay = movie.releaseDate;
        if(movie.releaseDate && movie.releaseDate.includes('T')) {
             const d = new Date(movie.releaseDate);
             dateDisplay = d.toLocaleString();
        }

        let isActuallyUpcoming = movie.isUpcoming;
        if (movie.isUpcoming) {
            let ts = movie.releaseTs;
            if (!ts && movie.releaseDate) ts = Date.parse(movie.releaseDate);
            if (ts && ts <= Date.now()) isActuallyUpcoming = false;
        }

        const upcomingBadge = isActuallyUpcoming ? 
            `<span class="bg-purple-900/50 text-purple-300 text-[10px] px-1 rounded ml-1 border border-purple-500/30">UPCOMING</span>` : '';

        const privateBadge = movie.isPrivate ? 
            `<span class="bg-gray-900/50 text-gray-400 text-[10px] px-1 rounded ml-1 border border-gray-500/30">PRIVATE</span>` : '';

        const typeBadge = (movie.contentType === 'series') ? 
            `<span class="bg-indigo-900/50 text-indigo-300 text-[10px] px-2 py-0.5 rounded border border-indigo-500/30">SERIES</span>` : 
            `<span class="bg-gray-700 text-gray-300 text-[10px] px-2 py-0.5 rounded">MOVIE</span>`;

        return `
        <tr class="border-b border-gray-700 hover:bg-gray-700/50 transition-colors">
            <td class="p-3"><img src="${movie.posterUrl}" class="w-10 h-14 object-cover rounded shadow"></td>
            <td class="p-3 font-medium text-white whitespace-nowrap overflow-hidden text-ellipsis max-w-[150px]">
                ${movie.title}
                <br>${upcomingBadge} ${privateBadge}
            </td>
            <td class="p-3">${typeBadge}</td>
            <td class="p-3"><span class="text-xs ${movie.isPrivate ? 'text-gray-500' : 'text-green-400'}">${movie.isPrivate ? 'Private' : 'Public'}</span></td>
            <td class="p-3 text-blue-400 text-xs"><span class="bg-blue-900/30 px-2 py-1 rounded">${[MX.typeOf(movie), movie.industry].filter(Boolean).join(' · ')}</span><br><span class="text-[10px] text-gray-400">${MX.genresOf(movie).join(', ') || '—'}</span></td>
            <td class="p-3 text-xs text-gray-400">${movie.year}</td>
            <td class="p-3 text-right">
                <button onclick="editMovie('${id}')" class="bg-blue-600/20 text-blue-400 hover:bg-blue-600 hover:text-white p-2 rounded-lg mr-2"><i class="fas fa-edit"></i></button>
                <button onclick="deleteItem('movies', '${id}')" class="bg-red-600/20 text-red-400 hover:bg-red-600 hover:text-white p-2 rounded-lg"><i class="fas fa-trash"></i></button>
            </td>
        </tr>
    `}).join('');
}

window.filterMovies = (type, value) => {
    if(type === 'type') {
        currentTypeFilter = value;
        document.querySelectorAll('[data-type-filter]').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.typeFilter === value);
            btn.classList.toggle('inactive', btn.dataset.typeFilter !== value);
        });
    } else if(type === 'status') {
        currentStatusFilter = value;
        document.querySelectorAll('[data-status-filter]').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.statusFilter === value);
            btn.classList.toggle('inactive', btn.dataset.statusFilter !== value);
        });
    } else if(type === 'upcoming') {
        currentUpcomingFilter = value;
        document.querySelectorAll('[data-upcoming-filter]').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.upcomingFilter === value);
            btn.classList.toggle('inactive', btn.dataset.upcomingFilter !== value);
        });
    } else if(type === 'category') {
        currentCategoryFilter = value;
    } else if(type === 'industry') {
        currentIndustryFilter = value;
    } else if(type === 'lang') {
        currentLangFilter = value;
    } else if(type === 'year') {
        currentYearFilter = value;
    }

    renderAllMoviesTable(allMoviesCache);
};
