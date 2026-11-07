// --- HERO BANNER SLIDER (replaces 3D carousel) ---
let heroTimer;
const renderBanners = (banners) => {
    carouselEl = document.getElementById('carousel');
    if (!banners || !carouselEl) return;
    const list = Object.values(banners);
    if (!list.length) return;

    carouselEl.innerHTML = list.map((b, i) => {
        const m = (b.movieId && allMovies[b.movieId]) || {};
        const img = b.imageUrl || m.coverUrl || m.posterUrl || '';
        const desc = m.description || m.overview || m.plot || '';
        return `<div class="hero-slide ${i === 0 ? 'on' : ''}">
            <img src="${esc(img)}" alt="">
            <div class="hero-info">
                <span class="hero-tag">${m.contentType === 'series' ? 'Series' : 'Featured'}</span>
                <h2 class="hero-title">${esc(m.title || b.title || '')}</h2>
                <div class="hero-meta">${m.year ? `<span>${esc(m.year)}</span>` : ''}${m.rating ? `<span><i class="fas fa-star" style="color:#facc15"></i> ${esc(m.rating)}</span>` : ''}${m.category ? `<span>${esc(m.category)}</span>` : ''}</div>
                ${desc ? `<p class="hero-desc">${esc(desc)}</p>` : ''}
                ${b.movieId ? `<div class="hero-actions"><button class="hero-btn" onclick="showMovieDetails('${esc(b.movieId)}')"><i class="fas fa-play"></i> Play</button><button class="hero-btn ghost" onclick="showMovieDetails('${esc(b.movieId)}')"><i class="fas fa-info-circle"></i> Info</button></div>` : ''}
            </div>
        </div>`;
    }).join('') + `<div class="hero-dots">${list.map((_, i) => `<i data-i="${i}" class="${i === 0 ? 'on' : ''}"></i>`).join('')}</div>`;

    const slides = carouselEl.querySelectorAll('.hero-slide');
    const dots = carouselEl.querySelectorAll('.hero-dots i');
    let cur = 0;
    const go = (n) => {
        cur = (n + slides.length) % slides.length;
        slides.forEach((s, i) => s.classList.toggle('on', i === cur));
        dots.forEach((d, i) => d.classList.toggle('on', i === cur));
    };
    dots.forEach(d => d.onclick = () => { go(+d.dataset.i); start(); });
    const start = () => { clearInterval(heroTimer); if (slides.length > 1) heroTimer = setInterval(() => go(cur + 1), 5000); };
    start();
};
function rotateCarousel() {}

window.addEventListener('scroll', () => {
    const h = document.getElementById('global-header');
    if (h) h.classList.toggle('solid', window.scrollY > 40);
}, { passive: true });
