// --- CUSTOM MODAL SYSTEM ---
window.showCustomModal = function(type, message, onConfirmCallback = null) {
    const overlay = document.getElementById('custom-modal-overlay');
    const box = document.getElementById('custom-modal-box');
    const icon = document.getElementById('custom-modal-icon');
    const title = document.getElementById('custom-modal-title');
    const msgEl = document.getElementById('custom-modal-message');
    const btnContainer = document.getElementById('custom-modal-buttons');

    msgEl.textContent = message;
    btnContainer.innerHTML = '';

    overlay.classList.remove('hidden');

    const closeModal = () => {
        overlay.classList.add('opacity-0');
        box.classList.add('scale-95');
        setTimeout(() => { overlay.classList.add('hidden'); }, 300);
    };

    if (type === 'alert') {
        icon.className = "w-16 h-16 rounded-full mx-auto flex items-center justify-center mb-4 border shadow-inner bg-amber-500/10 border-amber-500/30";
        icon.innerHTML = '<i class="fas fa-exclamation-triangle text-3xl text-amber-500"></i>';
        title.textContent = 'Notice';
        btnContainer.innerHTML = `<button id="c-modal-ok" class="w-full bg-blue-600 hover:bg-blue-700 text-white font-bold py-2.5 rounded-xl shadow-lg transition-all border border-blue-500/30">OK</button>`;

        document.getElementById('c-modal-ok').onclick = closeModal;
    } else if (type === 'confirm') {
        icon.className = "w-16 h-16 rounded-full mx-auto flex items-center justify-center mb-4 border shadow-inner bg-rose-500/10 border-rose-500/30";
        icon.innerHTML = '<i class="fas fa-trash-alt text-3xl text-rose-500"></i>';
        title.textContent = 'Are you sure?';
        btnContainer.innerHTML = `
            <button id="c-modal-cancel" class="flex-1 bg-slate-700 hover:bg-slate-600 text-white font-semibold py-2.5 rounded-xl transition-all border border-slate-600/50">Cancel</button>
            <button id="c-modal-confirm" class="flex-1 bg-rose-600 hover:bg-rose-700 text-white font-semibold py-2.5 rounded-xl shadow-[0_0_15px_rgba(225,29,72,0.4)] transition-all border border-rose-500/30">Delete</button>
        `;

        document.getElementById('c-modal-cancel').onclick = closeModal;
        document.getElementById('c-modal-confirm').onclick = () => {
            closeModal();
            if (onConfirmCallback) onConfirmCallback();
        };
    }

    setTimeout(() => {
        overlay.classList.remove('opacity-0');
        box.classList.remove('scale-95');
    }, 10);
};

