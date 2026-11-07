// --- Editable Socials + Notifications (unchanged) ---
function initEditableSocials(){
    const form = document.getElementById('social-form');
    const nameEl = document.getElementById('social-name');
    const urlEl  = document.getElementById('social-url');
    const iconEl = document.getElementById('social-icon');
    const presetEl = document.getElementById('social-icon-preset');
    const previewEl = document.getElementById('social-icon-preview');
    const submitBtn = document.getElementById('social-submit-btn');
    const cancelBtn = document.getElementById('social-cancel-btn');

    if(!form) return;

    const syncIcon = () => {
        const val = (iconEl.value || presetEl.value || 'fas fa-link').trim();
        previewEl.className = val + ' text-blue-400';
    };
    presetEl?.addEventListener('change', ()=>{ iconEl.value = presetEl.value; syncIcon(); });
    iconEl?.addEventListener('input', syncIcon);
    syncIcon();

    const reset = () => {
        form.dataset.editId = '';
        submitBtn.textContent = 'Add Link';
        cancelBtn.classList.add('hidden');
        nameEl.value = '';
        urlEl.value = '';
        presetEl.value = 'fas fa-link';
        iconEl.value = '';
        syncIcon();
    };

    cancelBtn?.addEventListener('click', reset);

    form.addEventListener('submit', (e)=>{
        e.preventDefault();
        const data = {
            name: (nameEl.value||'').trim(),
            url: (urlEl.value||'').trim(),
            icon: (iconEl.value||presetEl.value||'fas fa-link').trim()
        };
        if(!data.name || !data.url) return alert('Name and URL required');
        const editId = form.dataset.editId;
        const ref = editId ? db.ref(`socialLinks/${editId}`) : db.ref('socialLinks').push();
        ref.set(data).then(()=> reset());
    });

    window.editSocial = (id, item) => {
        form.dataset.editId = id;
        nameEl.value = item?.name || '';
        urlEl.value = item?.url || '';
        const icon = (item?.icon || 'fas fa-link').trim();
        presetEl.value = icon;
        iconEl.value = icon;
        syncIcon();
        submitBtn.textContent = 'Update Link';
        cancelBtn.classList.remove('hidden');
        showAdminPage('settings');
        setTimeout(() => {
            document.getElementById('social-section').scrollIntoView({ behavior: 'smooth', block: 'start' });
        }, 100);
    };

    db.ref('socialLinks').on('value', s=>{
        const data = s.val() || {};
        window.__tmp_socials = data;
        const listEl = document.getElementById('socials-list');
        if(!listEl) return;
        const entries = Object.entries(data);
        listEl.innerHTML = entries.length ? entries.map(([id,i])=>`
            <div class="flex items-center justify-between bg-gray-700/50 p-2 rounded border border-gray-600">
                <div class="flex items-center gap-2">
                    <i class="${(i.icon||'fas fa-link')} text-blue-400"></i>
                    <div>
                        <div class="text-sm font-semibold">${i.name||''}</div>
                        <div class="text-[10px] text-gray-400 truncate max-w-[260px]">${i.url||''}</div>
                    </div>
                </div>
                <div class="flex items-center gap-2">
                    <button onclick='editSocial("${id}", (window.__tmp_socials||{})["${id}"])' class="text-xs bg-gray-600 hover:bg-gray-500 px-2 py-1 rounded text-white">
                        <i class="fas fa-pen"></i>
                    </button>
                    <button onclick="deleteItem('socialLinks','${id}')" class="text-red-400 hover:text-red-300">
                        <i class="fas fa-trash"></i>
                    </button>
                </div>
            </div>
        `).join('') : '<p class="text-xs text-gray-500">No social links yet.</p>';
    });

    reset();
}

function initEditableNotifications(){
    const form = document.getElementById('notification-form');
    const titleEl = document.getElementById('notif-title');
    const msgEl = document.getElementById('notif-message');
    const submitBtn = document.getElementById('notif-submit-btn');
    const cancelBtn = document.getElementById('notif-cancel-btn');

    if(!form) return;

    const reset = () => {
        form.dataset.editId = '';
        submitBtn.textContent = 'Send';
        cancelBtn.classList.add('hidden');
        titleEl.value = '';
        msgEl.value = '';
    };
    cancelBtn?.addEventListener('click', reset);

    form.addEventListener('submit', (e)=>{
        e.preventDefault();
        const data = { title: (titleEl.value||'').trim(), message: (msgEl.value||'').trim(), timestamp: Date.now() };
        if(!data.title || !data.message) return alert('Title and message required');
        const editId = form.dataset.editId;
        const ref = editId ? db.ref(`notifications/${editId}`) : db.ref('notifications').push();
        ref.set(data).then(()=> reset());
    });

    window.editNotif = (id, item) => {
        form.dataset.editId = id;
        titleEl.value = item?.title || '';
        msgEl.value = item?.message || '';
        submitBtn.textContent = 'Update';
        cancelBtn.classList.remove('hidden');
        showAdminPage('settings');
        setTimeout(() => {
            document.getElementById('notification-section').scrollIntoView({ behavior: 'smooth', block: 'start' });
        }, 100);
    };

    db.ref('notifications').on('value', s=>{
        const data = s.val() || {};
        window.__tmp_notifs = data;
        const listEl = document.getElementById('notifications-list');
        if(!listEl) return;
        const entries = Object.entries(data).sort((a,b)=>(b[1].timestamp||0)-(a[1].timestamp||0));
        listEl.innerHTML = entries.length ? entries.map(([id,i])=>`
            <div class="bg-gray-700/50 p-3 rounded border border-gray-600 relative">
                <h5 class="font-bold text-xs pr-20">${i.title||''}</h5>
                <p class="text-xs text-gray-300 mt-1">${i.message||''}</p>
                <p class="text-[10px] text-gray-500 mt-2 text-right">${i.timestamp?new Date(i.timestamp).toLocaleString():''}</p>
                <div class="absolute top-2 right-2 flex gap-2">
                    <button onclick='editNotif("${id}", (window.__tmp_notifs||{})["${id}"])' class="text-xs bg-gray-600 hover:bg-gray-500 px-2 py-1 rounded text-white">
                        <i class="fas fa-pen"></i>
                    </button>
                    <button onclick="deleteItem('notifications','${id}')" class="text-red-400 hover:text-red-300">
                        <i class="fas fa-trash"></i>
                    </button>
                </div>
            </div>
        `).join('') : '<p class="text-xs text-gray-500">No notifications yet.</p>';
    });

    reset();
}
