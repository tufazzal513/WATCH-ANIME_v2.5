// --- RENDER SOCIALS & NOTIFICATIONS ---
const renderSocials = (socials) => {
    const container = document.getElementById('social-links-container');
    if(!container) return;
    if(!socials) { container.innerHTML = '<p class="text-xs text-gray-500 col-span-full text-center py-5 bg-slate-800/30 rounded-xl border border-slate-700/50">No community links added yet.</p>'; return; }

    container.innerHTML = Object.values(socials).map((item, index) => `
        <a href="${item.url}" target="_blank" class="group flex items-center bg-slate-800/60 backdrop-blur-sm p-4 rounded-xl hover:bg-slate-700/80 transition-all duration-300 border border-slate-700/80 hover:border-blue-500/50 shadow-lg hover:shadow-[0_0_15px_rgba(37,99,235,0.15)] transform hover:-translate-y-1 animate-slide-up" style="animation-delay: ${index * 0.08}s;">
            <div class="w-12 h-12 rounded-full bg-blue-900/40 flex items-center justify-center mr-4 text-blue-400 group-hover:scale-110 group-hover:bg-blue-600 group-hover:text-white transition-all duration-300 border border-blue-500/20 shadow-inner">
                <i class="${item.icon || 'fas fa-link'} text-xl"></i>
            </div>
            <div class="flex-1 min-w-0">
                <p class="font-bold text-sm text-gray-200 group-hover:text-white truncate">${item.name}</p>
                <p class="text-[10px] text-gray-400 mt-0.5 uppercase tracking-wide">Tap to join</p>
            </div>
            <div class="w-8 h-8 rounded-full bg-slate-800 flex items-center justify-center border border-slate-600 text-gray-500 group-hover:border-blue-500/50 group-hover:text-blue-400 transition-colors">
                <i class="fas fa-chevron-right text-[10px]"></i>
            </div>
        </a>
    `).join('');
};

const renderNotifications = (notifs) => {
    const container = document.getElementById('notifications-container');
    if(!container) return;
    if(!notifs) { container.innerHTML = '<div class="text-center py-8 bg-slate-800/30 rounded-xl border border-slate-700/50"><i class="fas fa-box-open text-3xl mb-2 text-gray-600"></i><p class="text-xs text-gray-400 font-medium">No announcements right now.</p></div>'; return; }

    const list = Object.values(notifs).sort((a,b) => b.timestamp - a.timestamp);

    container.innerHTML = list.map((item, index) => {
        const dateObj = new Date(item.timestamp);
        const dateStr = dateObj.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });

        return `
        <div class="relative overflow-hidden bg-slate-800/70 backdrop-blur-sm p-5 rounded-xl border border-slate-700/60 hover:border-blue-500/40 shadow-lg transition-all duration-300 transform hover:-translate-y-1 animate-slide-up" style="animation-delay: ${index * 0.1}s;">
            <!-- Elegant Side Border -->
            <div class="absolute left-0 top-0 w-1 h-full bg-gradient-to-b from-blue-500 to-purple-500"></div>

            <div class="flex justify-between items-start mb-3 pl-2">
                <div class="flex items-center gap-3 pr-2">
                    <div class="w-8 h-8 rounded-full bg-blue-500/10 flex items-center justify-center text-blue-400 border border-blue-500/20 shadow-inner flex-shrink-0">
                        <i class="fas fa-bullhorn text-xs"></i>
                    </div>
                    <h4 class="font-bold text-sm text-white tracking-wide leading-tight">${item.title}</h4>
                </div>
                <span class="text-[9px] font-bold uppercase tracking-widest text-blue-300 bg-blue-900/30 px-2.5 py-1 rounded-md border border-blue-800/50 whitespace-nowrap flex-shrink-0">${dateStr}</span>
            </div>

            <p class="text-xs text-gray-300 leading-relaxed pl-11 pr-2">${item.message}</p>
        </div>
        `;
    }).join('');
};
