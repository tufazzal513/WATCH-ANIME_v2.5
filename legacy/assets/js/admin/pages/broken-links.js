// --- BROKEN LINKS LOGIC ---
db.ref('broken_links').on('value', snapshot => {
    allBrokenLinksCache = snapshot.val() || {};
    renderBrokenLinksTable();

    const count = Object.keys(allBrokenLinksCache).length;
    const badge = document.getElementById('broken-badge');
    if(count > 0) {
        badge.textContent = count;
        badge.classList.remove('hidden');
    } else {
        badge.classList.add('hidden');
    }
});

document.getElementById('broken-search-input').addEventListener('input', () => renderBrokenLinksTable());

window.renderBrokenLinksTable = function() {
    const container = document.getElementById('broken-links-table');
    const searchTerm = document.getElementById('broken-search-input').value.toLowerCase();
    const typeFilter = document.getElementById('broken-type-filter').value;

    let data = Object.values(allBrokenLinksCache);

    if(typeFilter !== 'all') {
        data = data.filter(item => item.type === typeFilter);
    }
    if(searchTerm) {
        data = data.filter(item => item.title.toLowerCase().includes(searchTerm) || item.epInfo.toLowerCase().includes(searchTerm));
    }

    if(data.length === 0) {
        container.innerHTML = '<tr><td colspan="5" class="p-8 text-center text-gray-500 font-bold"><i class="fas fa-check-circle text-green-500 text-2xl mb-2"></i><br>No broken links reported. Awesome! 🎉</td></tr>';
        return;
    }

    container.innerHTML = data.sort((a,b) => b.reportedAt - a.reportedAt).map(item => {
        const date = new Date(item.reportedAt).toLocaleString();
        const typeBadge = item.type === 'series' ? `<span class="bg-purple-900/50 text-purple-300 text-[10px] px-1.5 py-0.5 rounded border border-purple-500/30">SERIES</span>` : `<span class="bg-blue-900/50 text-blue-300 text-[10px] px-1.5 py-0.5 rounded border border-blue-500/30">MOVIE</span>`;

        return `
        <tr class="border-b border-gray-700 hover:bg-gray-700/50 transition-colors">
            <td class="p-3"><img src="${item.posterUrl}" class="w-10 h-14 object-cover rounded shadow"></td>
            <td class="p-3">
                <div class="font-bold text-white mb-1">${item.title} ${typeBadge}</div>
                <div class="text-[10px] text-gray-400">Reported: ${date}</div>
            </td>
            <td class="p-3 text-red-400 font-bold text-sm">${item.epInfo}</td>
            <td class="p-3 text-xs text-gray-300 uppercase"><i class="fas fa-server mr-1 text-blue-400"></i> ${item.serverType}</td>
            <td class="p-3 text-right">
                <button onclick="fixBrokenLink('${item.movieId}')" class="bg-green-600/20 text-green-400 hover:bg-green-600 hover:text-white px-3 py-1.5 rounded-lg text-xs font-bold mr-2 transition-colors border border-green-500/30 hover:border-transparent shadow-sm"><i class="fas fa-wrench mr-1"></i> Fix</button>
                <button onclick="deleteItem('broken_links', '${item.movieId}')" class="bg-gray-800 text-gray-400 hover:bg-red-600 hover:text-white p-1.5 rounded-lg transition-colors border border-gray-600 hover:border-transparent"><i class="fas fa-check"></i></button>
            </td>
        </tr>
        `;
    }).join('');
};

window.fixBrokenLink = function(movieId) {
    editMovie(movieId); // Existing function opens the edit content page directly!
    // Close sidebar if open
    document.getElementById('admin-sidebar').classList.remove('active');
    document.getElementById('sidebar-overlay').classList.remove('active');
};
