// --- LINK AUTO DETECTOR ---
window.triggerLinkDetectors = function() {}; 
function initLinkDetector() {
    const detectVideoType = (url) => {
        if (!url) return '';
        url = url.toLowerCase();
        if (url.includes('youtube.com') || url.includes('youtu.be')) return '<span class="bg-red-500/20 text-red-400 border border-red-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fab fa-youtube"></i> YOUTUBE</span>';
        if (url.includes('vimeo.com')) return '<span class="bg-blue-500/20 text-blue-400 border border-blue-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fab fa-vimeo"></i> VIMEO</span>';
        if (url.includes('videasy.net')) return '<span class="bg-purple-500/20 text-purple-400 border border-purple-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fas fa-play-circle"></i> VIDEASY</span>';
        if (url.includes('/embed') || url.includes('drive.google.com')) return '<span class="bg-indigo-500/20 text-indigo-400 border border-indigo-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fas fa-code"></i> EMBED/IFRAME</span>';
        if (url.includes('dropbox.com')) return '<span class="bg-blue-500/20 text-blue-400 border border-blue-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fab fa-dropbox"></i> DROPBOX</span>';
        if (/\.m3u8(\?|#|$)/i.test(url)) return '<span class="bg-green-500/20 text-green-400 border border-green-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fas fa-stream"></i> HLS STREAM</span>';
        if (url.includes('.mp4') || url.includes('.mkv')) return '<span class="bg-green-500/20 text-green-400 border border-green-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fas fa-file-video"></i> DIRECT VIDEO</span>';
        return '<span class="bg-gray-500/20 text-gray-400 border border-gray-500/30 px-1.5 py-0.5 rounded text-[9px] ml-2 shadow-sm"><i class="fas fa-link"></i> CUSTOM URL</span>';
    };['movie-server1', 'edit-ep-server1'].forEach(id => {
        const input = document.getElementById(id);
        if (!input) return;

        const label = input.parentElement.querySelector('label');
        if(!label) return;

        const badgeSpan = document.createElement('span');
        badgeSpan.className = 'transition-all duration-300 opacity-0 inline-block translate-y-1';
        label.appendChild(badgeSpan);

        const updateBadge = () => {
            const val = input.value.trim();
            if (val) {
                badgeSpan.innerHTML = detectVideoType(val);
                badgeSpan.classList.remove('opacity-0', 'translate-y-1');
            } else {
                badgeSpan.classList.add('opacity-0', 'translate-y-1');
            }
        };

        input.addEventListener('input', updateBadge);
        input.addEventListener('change', updateBadge);
    });

    window.triggerLinkDetectors = () => {['movie-server1', 'edit-ep-server1'].forEach(id => {
            const input = document.getElementById(id);
            if(input) input.dispatchEvent(new Event('input'));
        });
    };
}

function startUpcomingChecker() {
    checkUpcomingMovies();
    setInterval(checkUpcomingMovies, 60000);
}

function checkUpcomingMovies() {
    const now = Date.now();
    db.ref('movies').once('value', (snapshot) => {
        const movies = snapshot.val() || {};
        const updates = {};
        Object.entries(movies).forEach(([id, movie]) => {
            if (movie.isUpcoming) {
                let ts = movie.releaseTs;
                if (!ts && movie.releaseDate) {
                    ts = Date.parse(movie.releaseDate);
                }
                if (ts && ts <= now) {
                    updates[`movies/${id}/isUpcoming`] = false;
                }
            }
        });
        if (Object.keys(updates).length > 0) {
            db.ref().update(updates);
        }
    });
}
