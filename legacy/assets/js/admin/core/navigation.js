window.toggleSidebar = function() {
    document.getElementById('admin-sidebar').classList.toggle('active');
    document.getElementById('sidebar-overlay').classList.toggle('active');
};

window.logout = function() {
    try { 
        localStorage.removeItem('movex_admin_cache');
        auth.signOut().then(() => window.location.reload()); 
    } catch(e) { console.error(e); }
};

// Page navigation (With Refresh Memory & Back Button Support)
const showAdminPage = (pageId, saveHistory = true) => {
    adminPages.forEach(p => p.classList.toggle('active', p.id === pageId));
    adminNavBtns.forEach(b => {
        if (b.dataset.page === pageId) {
            b.classList.add('text-blue-500');
            b.classList.remove('text-gray-400');
        } else {
            b.classList.remove('text-blue-500');
            b.classList.add('text-gray-400');
        }
    });
    window.scrollTo(0,0);

    // ব্রাউজারের ক্যাশে বর্তমান পেজটি সেভ করে রাখা হচ্ছে
    localStorage.setItem('movex_last_page', pageId);

    // ব্যাক বাটনের জন্য ব্রাউজারের হিস্ট্রি আপডেট করা হচ্ছে
    if(saveHistory) {
        window.history.pushState({page: pageId}, '', '#' + pageId);
    }
};

// ব্রাউজারের বা ফোনের Back/Forward বাটন চাপলে যেন কাজ করে
window.addEventListener('popstate', (e) => {
    if(e.state && e.state.page) {
        showAdminPage(e.state.page, false);
    } else {
        const hash = window.location.hash.replace('#', '');
        if(hash && document.getElementById(hash) && document.getElementById(hash).classList.contains('admin-page')) {
            showAdminPage(hash, false);
        } else {
            const lastPage = localStorage.getItem('movex_last_page') || 'dashboard';
            showAdminPage(lastPage, false);
        }
    }
});

adminNavBtns.forEach(b => b.addEventListener('click', () => showAdminPage(b.dataset.page)));
