// Banner logic (unchanged)
const bannerSearchInput = document.getElementById('banner-movie-search');
const bannerResultsDiv = document.getElementById('banner-search-results');
const bannerSelectedId = document.getElementById('banner-selected-movie-id');
const bannerUrlInput = document.getElementById('banner-url');
const bannerPreviewDiv = document.getElementById('banner-selection-preview');
const bannerSelectedTitle = document.getElementById('banner-selected-title');

bannerSearchInput.addEventListener('input', (e) => {
    const term = e.target.value.toLowerCase();
    bannerResultsDiv.innerHTML = '';
    if(term.length < 2) { bannerResultsDiv.classList.add('hidden'); return; }
    const results = Object.entries(allMoviesCache).filter(([id, m]) => m.title.toLowerCase().includes(term));
    if(results.length > 0) {
        bannerResultsDiv.classList.remove('hidden');
        results.forEach(([id, m]) => {
            const div = document.createElement('div');
            // Premium Search Dropdown Item
            div.className = 'p-3 cursor-pointer flex items-center gap-3 border-b border-gray-700 hover:bg-gray-700 transition-colors last:border-0';
            div.innerHTML = `
                <img src="${m.posterUrl}" class="w-10 h-14 object-cover rounded shadow-md border border-gray-600"> 
                <div class="flex flex-col">
                    <span class="text-sm font-bold text-white">${m.title}</span>
                    <span class="text-[10px] text-gray-400 bg-gray-900/50 px-1.5 py-0.5 rounded w-max mt-1 border border-gray-700">${(m.contentType||'movie').toUpperCase()} • ${m.year||'N/A'}</span>
                </div>
            `;
            div.addEventListener('click', () => {
                bannerSelectedId.value = id;
                bannerSelectedTitle.textContent = m.title;
                bannerUrlInput.value = m.posterUrl; 
                bannerPreviewDiv.classList.remove('hidden');
                bannerResultsDiv.classList.add('hidden');
                bannerSearchInput.value = '';
            });
            bannerResultsDiv.appendChild(div);
        });
    } else {
        bannerResultsDiv.classList.add('hidden');
    }
});
document.addEventListener('click', (e) => { if(!bannerSearchInput.contains(e.target) && !bannerResultsDiv.contains(e.target)) bannerResultsDiv.classList.add('hidden'); });

setupCrud('banners', 'banner-form', 'banners-list', (c,d) => {
     document.getElementById(c).innerHTML = Object.entries(d).map(([id, b]) => `
        <div class="relative group rounded-2xl overflow-hidden border border-gray-700/60 shadow-xl bg-gray-900 aspect-video transition-all transform hover:-translate-y-1 hover:shadow-[0_0_20px_rgba(20,184,166,0.2)] hover:border-teal-500/50 flex items-center justify-center">

            <!-- Cinematic Blurred Background (Solves the Crop Issue) -->
            <img src="${b.imageUrl}" class="absolute inset-0 w-full h-full object-cover blur-xl opacity-40 scale-110" alt="bg">

            <!-- Foreground Image (Perfectly Contained) -->
            <img src="${b.imageUrl}" class="absolute inset-0 w-full h-full object-contain z-10 drop-shadow-2xl transition-transform duration-500 group-hover:scale-105" alt="banner">

            <!-- Gradient Overlay for Text -->
            <div class="absolute inset-0 bg-gradient-to-t from-gray-900 via-gray-900/20 to-transparent z-20 opacity-80 group-hover:opacity-90 transition-opacity"></div>

            <!-- Movie Details Overlay -->
            <div class="absolute inset-x-0 bottom-0 p-4 z-30 flex justify-between items-end translate-y-2 group-hover:translate-y-0 transition-transform">
                <div class="flex-1 overflow-hidden pr-2">
                    <span class="inline-block px-2 py-0.5 bg-teal-500/20 border border-teal-500/30 rounded text-[9px] font-bold text-teal-300 uppercase tracking-widest mb-1.5 backdrop-blur-sm"><i class="fas fa-link mr-1"></i>Linked Content</span>
                    <h4 class="text-sm font-bold text-white truncate drop-shadow-md">${b.movieTitle || 'No Content Linked'}</h4>
                </div>
            </div>

            <!-- Premium Delete Button (Shows on Hover) -->
            <button onclick="deleteItem('banners', '${id}')" class="absolute top-3 right-3 z-40 bg-gray-900/80 hover:bg-red-600 text-gray-300 hover:text-white w-8 h-8 rounded-full flex items-center justify-center backdrop-blur-md border border-gray-600 hover:border-red-500 transition-all opacity-0 group-hover:opacity-100 shadow-lg transform scale-90 group-hover:scale-100">
                <i class="fas fa-trash text-xs"></i>
            </button>
        </div>`).join('');
}, () => { 
    const mid = bannerSelectedId.value; const m = allMoviesCache[mid]; 
    const fd = { imageUrl: bannerUrlInput.value, movieId: mid||null, movieTitle: m?m.title:null };
    // Reset fields after successful submit
    bannerSelectedId.value=''; bannerPreviewDiv.classList.add('hidden'); bannerUrlInput.value=''; document.getElementById('banner-movie-search').value=''; return fd; 
});

// Settings App Details
