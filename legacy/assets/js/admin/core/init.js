
function initAdminPanel() {
    // URL Hash অথবা Local Storage থেকে সর্বশেষ দেখা পেজটি খুঁজে বের করা
    const hashPage = window.location.hash.replace('#', '');
    const lastPage = hashPage || localStorage.getItem('movex_last_page') || 'dashboard';

    // পেজটি আসলেই আছে কিনা তা চেক করা, না থাকলে ডিফল্ট ড্যাশবোর্ডে যাবে
    const pageExists = document.getElementById(lastPage) && document.getElementById(lastPage).classList.contains('admin-page');
    const targetPage = pageExists ? lastPage : 'dashboard';

    showAdminPage(targetPage, false);
    history.replaceState({ page: targetPage }, '', '#' + targetPage);

    updateDashboard();
    initEditableSocials();
    initEditableNotifications();
    initUserManagement(); // new
    startUpcomingChecker();
    initLinkDetector(); // Auto link detector চালু করা হলো
    // Initially hide edit panels
    editPanel.classList.add('hidden');
    document.getElementById('user-edit-panel').classList.add('hidden');
}
