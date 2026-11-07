// ========== USER MANAGEMENT FUNCTIONS ==========
// Listen to users and online_users
function initUserManagement() {
    // Listen to users node
    db.ref('users').on('value', (snapshot) => {
        allUsersCache = snapshot.val() || {};
        renderUsersTable();

        // Update popup instantly if data changes (e.g. frozen status)
        const panel = document.getElementById('user-edit-panel');
        if(!panel.classList.contains('hidden') && panel.dataset.uid) {
            const updatedUser = allUsersCache[panel.dataset.uid];
            if(updatedUser) {
                const freezeBtnText = document.getElementById('freeze-btn-text');
                const freezeIcon = document.getElementById('freeze-icon');
                if(updatedUser.status === 'frozen') {
                    freezeBtnText.textContent = "Unfreeze User";
                    freezeIcon.className = "fas fa-fire text-amber-400";
                } else {
                    freezeBtnText.textContent = "Freeze User";
                    freezeIcon.className = "fas fa-snowflake text-white";
                }
                const status = getUserStatus(panel.dataset.uid, updatedUser);
                document.getElementById('user-detail-status-badge').innerHTML = getStatusBadge(status);
            }
        }
    });

    // Listen to online_users node for live status
    db.ref('online_users').on('value', (snapshot) => {
        onlineUsersCache = snapshot.val() || {};
        renderUsersTable(); // re-render to update live status

        // Update last seen in popup instantly
        const panel = document.getElementById('user-edit-panel');
        if(!panel.classList.contains('hidden') && panel.dataset.uid) {
            const uid = panel.dataset.uid;
            const online = onlineUsersCache[uid];
            const user = allUsersCache[uid] || {};
            const lastSeenMs = online?.last_seen || user?.last_seen || user?.lastLoginAt;
            const lastSeen = lastSeenMs ? new Date(lastSeenMs).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short' }) : 'Never online / Hidden';
            document.getElementById('user-detail-lastseen').textContent = lastSeen;
        }
    });

    // Search input
    document.getElementById('user-search-input').addEventListener('input', (e) => {
        currentUserSearchTerm = e.target.value.toLowerCase();
        renderUsersTable();
    });

    // User edit panel close button
    document.getElementById('user-close-panel').addEventListener('click', () => {
        document.getElementById('user-edit-panel').classList.add('hidden');
    });

    // Toggle freeze button
    document.getElementById('user-toggle-freeze').addEventListener('click', () => {
        const uid = document.getElementById('user-edit-panel').dataset.uid;
        if (!uid) return;
        const user = allUsersCache[uid];
        if (!user) return;

        // Toggle status between active and frozen
        const newStatus = (user.status === 'frozen') ? 'active' : 'frozen';
        db.ref(`users/${uid}/status`).set(newStatus).then(() => {
            // System will auto update via listener and log user out
        });
    });

    // Delete User & Data button
    document.getElementById('user-toggle-delete').addEventListener('click', () => {
        const uid = document.getElementById('user-edit-panel').dataset.uid;
        if (!uid) return;

        if(confirm("Are you sure you want to permanently delete this user? All their data will be removed and they will be logged out instantly. They must create a new account to use the app again.")) {
            // Set status to 'deleted' to trigger Auth account auto-deletion on the user's side
            db.ref(`users/${uid}`).set({
                uid: uid,
                status: 'deleted',
                deletedAt: firebase.database.ServerValue.TIMESTAMP
            }).then(() => {
                db.ref(`online_users/${uid}`).remove();
                document.getElementById('user-edit-panel').classList.add('hidden');
            });
        }
    });
}

// Filter users by status
window.filterUsers = (type, value) => {
    if (type === 'status') {
        currentUserStatusFilter = value;
        document.querySelectorAll('[data-user-filter]').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.userFilter === value);
            btn.classList.toggle('inactive', btn.dataset.userFilter !== value);
        });
        renderUsersTable();
    }
};

// Determine user status for filtering and display
function getUserStatus(uid, user) {
    if (!user) return 'deleted'; 
    if (user.status === 'frozen') return 'frozen';

    // Check online
    const online = onlineUsersCache[uid];
    const now = Date.now();
    const LIVE_WINDOW = 90 * 1000;
    if (online && online.last_seen && (now - online.last_seen) <= LIVE_WINDOW) {
        return 'live';
    }
    // New (7d)
    if (user.createdAt) {
        const join = typeof user.createdAt === 'number' ? user.createdAt : Date.parse(user.createdAt);
        if (join && (now - join) < 7 * 24 * 60 * 60 * 1000) {
            return 'new';
        }
    }
    return 'active';
}

function getStatusBadge(status) {
    let statusBadge = '';
    switch(status) {
        case 'live': statusBadge = '<span class="bg-green-500/20 text-green-400 text-xs px-3 py-1 rounded-full border border-green-500/50 shadow-[0_0_10px_rgba(34,197,94,0.3)] animate-pulse"><i class="fas fa-circle text-[8px] mr-1"></i> LIVE NOW</span>'; break;
        case 'active': statusBadge = '<span class="bg-blue-500/20 text-blue-400 text-xs px-3 py-1 rounded-full border border-blue-500/50">ACTIVE</span>'; break;
        case 'frozen': statusBadge = '<span class="bg-orange-500/20 text-orange-400 text-xs px-3 py-1 rounded-full border border-orange-500/50"><i class="fas fa-snowflake mr-1"></i> FROZEN</span>'; break;
        case 'deleted': statusBadge = '<span class="bg-red-500/20 text-red-400 text-xs px-3 py-1 rounded-full border border-red-500/50"><i class="fas fa-ban mr-1"></i> DELETED</span>'; break;
        case 'new': statusBadge = '<span class="bg-purple-500/20 text-purple-400 text-xs px-3 py-1 rounded-full border border-purple-500/50">NEW USER</span>'; break;
        default: statusBadge = '<span class="bg-gray-600/30 text-gray-400 text-[10px] px-2 py-1 rounded-full">UNKNOWN</span>';
    }
    return statusBadge;
}

function renderUsersTable() {
    const tbody = document.getElementById('users-table-body');
    if (!tbody) return;

    // ডিলিট হওয়া ইউজারদের মূল লিস্ট থেকেই বাদ দিয়ে দেওয়া হচ্ছে
    let entries = Object.entries(allUsersCache).filter(([uid, user]) => user && user.status !== 'deleted');

    // Apply status filter
    if (currentUserStatusFilter !== 'all') {
        entries = entries.filter(([uid, user]) => {
            const status = getUserStatus(uid, user);
            if (currentUserStatusFilter === 'live') return status === 'live';
            if (currentUserStatusFilter === 'active') return status === 'active';
            if (currentUserStatusFilter === 'frozen') return status === 'frozen';
            if (currentUserStatusFilter === 'new') return status === 'new';
            return true;
        });
    }

    // Apply search
    if (currentUserSearchTerm) {
        const term = currentUserSearchTerm.toLowerCase();
        entries = entries.filter(([uid, user]) => {
            const dName = user.name || user.displayName || "";
            const email = user.email || "";
            return dName.toLowerCase().includes(term) || email.toLowerCase().includes(term) || uid.toLowerCase().includes(term);
        });
    }

    if (entries.length === 0) {
        tbody.innerHTML = '<tr><td colspan="6" class="p-4 text-center text-gray-500">No users found.</td></tr>';
        return;
    }

    // Sort by Date Joined (Newest first)
    entries.sort((a, b) => (b[1].createdAt || 0) - (a[1].createdAt || 0));

    tbody.innerHTML = entries.map(([uid, user]) => {
        const status = getUserStatus(uid, user);
        const statusBadge = getStatusBadge(status).replace('text-xs px-3', 'text-[10px] px-2'); // Make smaller for table

        const joinDate = user.createdAt ? new Date(user.createdAt).toLocaleDateString() : 'Unknown';
        const displayName = user.name || user.displayName || 'Unnamed User';
        const email = user.email || 'No email';
        const initial = displayName.charAt(0).toUpperCase();

        // Generate User Initials Profile Picture for Table
        const avatarHtml = `<div class="w-9 h-9 rounded-full bg-gradient-to-tr from-blue-600 to-cyan-500 flex items-center justify-center text-white font-bold text-sm shadow-md border border-slate-700">${initial}</div>`;

        return `
        <tr class="border-b border-gray-700 hover:bg-gray-700/50 transition-colors">
            <td class="p-3">${avatarHtml}</td>
            <td class="p-3">
                <div class="font-medium text-white">${displayName}</div>
                <div class="text-xs text-gray-400">${email}</div>
            </td>
            <td class="p-3 text-xs text-gray-400 font-mono">${uid.slice(0,8)}...</td>
            <td class="p-3">${statusBadge}</td>
            <td class="p-3 text-xs text-gray-400">${joinDate}</td>
            <td class="p-3 text-right">
                <button onclick="editUser('${uid}')" class="bg-blue-600/20 text-blue-400 hover:bg-blue-600 hover:text-white p-2 rounded-lg"><i class="fas fa-eye"></i></button>
            </td>
        </tr>
    `}).join('');
}

// Edit user (show details panel)
window.editUser = (uid) => {
    const user = allUsersCache[uid];
    if (!user) return;

    const panel = document.getElementById('user-edit-panel');
    panel.dataset.uid = uid;

    const displayName = user.name || user.displayName || 'Unnamed User';
    const initial = displayName.charAt(0).toUpperCase();

    // Set Modal Details
    document.getElementById('user-detail-avatar').textContent = initial;
    document.getElementById('user-detail-name').textContent = displayName;
    document.getElementById('user-detail-email').textContent = user.email || 'No email provided';

    document.getElementById('user-detail-uid').textContent = uid;
    document.getElementById('user-detail-uid').title = uid; 

    // Fetch correct Creation Date
    const joinDate = user.createdAt ? new Date(user.createdAt).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short' }) : 'Unknown';
    document.getElementById('user-detail-join').textContent = joinDate;

    // Fetch correct Online Date
    const online = onlineUsersCache[uid];
    const lastSeenMs = online?.last_seen || user?.last_seen || user?.lastLoginAt;
    const lastSeen = lastSeenMs ? new Date(lastSeenMs).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short' }) : 'Never online / Hidden';
    document.getElementById('user-detail-lastseen').textContent = lastSeen;

    // Generate Correct Status
    const status = getUserStatus(uid, user);
    document.getElementById('user-detail-status-badge').innerHTML = getStatusBadge(status);

    const freezeBtnText = document.getElementById('freeze-btn-text');
    const freezeIcon = document.getElementById('freeze-icon');

    // Manage Freeze text correctly
    if(user.status === 'frozen') {
        freezeBtnText.textContent = "Unfreeze User";
        freezeIcon.className = "fas fa-fire text-amber-400";
    } else {
        freezeBtnText.textContent = "Freeze User";
        freezeIcon.className = "fas fa-snowflake text-white";
    }

    // Always show Hard Delete logic
    const deleteBtnText = document.getElementById('delete-btn-text');
    const deleteIcon = document.getElementById('delete-icon');
    deleteBtnText.textContent = "Delete User & Data";
    deleteIcon.className = "fas fa-user-slash text-white";

    // Show popup without scrolling
    panel.classList.remove('hidden');
};
