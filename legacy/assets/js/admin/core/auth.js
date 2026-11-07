// Auth state observer (Optimized with Fast Boot Caching)
auth.onAuthStateChanged(user => {
    if (user) {
        // Fast Boot: ব্রাউজার ক্যাশ চেক করে সাথে সাথে প্যানেল ওপেন করবে (No loading delay)
        if (localStorage.getItem('movex_admin_cache') === 'true') {
            document.getElementById('global-loader').style.display = 'none';
            loginScreen.style.display = 'none';
            adminPanel.classList.remove('hidden');
            if (!window.adminPanelInitialized) {
                window.adminPanelInitialized = true;
                initAdminPanel();
            }
        }

        // ব্যাকগ্রাউন্ডে সিকিউরিটি চেক (ডেটাবেজ ভেরিফিকেশন)
        db.ref(`settings/adminUIDs/${user.uid}`).once('value').then(snapshot => {
            document.getElementById('global-loader').style.display = 'none';
            if (snapshot.exists() && snapshot.val() === true) {
                localStorage.setItem('movex_admin_cache', 'true'); // ক্যাশ সেভ
                if (!window.adminPanelInitialized) {
                    loginScreen.style.display = 'none';
                    adminPanel.classList.remove('hidden');
                    window.adminPanelInitialized = true;
                    initAdminPanel();
                }
            } else {
                // যদি ভুয়া অ্যাডমিন হয়, তবে বের করে দেবে
                localStorage.removeItem('movex_admin_cache');
                window.adminPanelInitialized = false;
                auth.signOut();
                loginScreen.style.display = 'flex';
                adminPanel.classList.add('hidden');
                authErrorEl.textContent = "Access Denied: You are not registered as an Admin.";
            }
        }).catch(err => {
            document.getElementById('global-loader').style.display = 'none';
            if (!localStorage.getItem('movex_admin_cache')) {
                auth.signOut();
                loginScreen.style.display = 'flex';
                authErrorEl.textContent = "Database Error: Could not verify admin.";
            }
        });
    } else {
        localStorage.removeItem('movex_admin_cache');
        window.adminPanelInitialized = false;
        document.getElementById('global-loader').style.display = 'none';
        loginScreen.style.display = 'flex';
        adminPanel.classList.add('hidden');
    }
});

// ফেইলসেফ: ইন্টারনেট অনেক স্লো থাকলে বা এরর হলে ৫ সেকেন্ড পর লোডার অটো রিমুভ হবে
setTimeout(() => {
    const loader = document.getElementById('global-loader');
    if (loader && loader.style.display !== 'none') {
        loader.style.display = 'none';
    }
}, 5000);

document.getElementById('admin-login-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const email = document.getElementById('admin-email').value;
    const password = document.getElementById('admin-password').value;
    // লগইন করার সময় লোডার দেখানো
    document.getElementById('global-loader').style.display = 'flex';
    auth.signInWithEmailAndPassword(email, password).catch(err => {
        document.getElementById('global-loader').style.display = 'none';
        authErrorEl.textContent = err.message;
    });
});

document.getElementById('admin-logout-sidebar-btn').addEventListener('click', () => {
    localStorage.removeItem('movex_admin_cache');
    auth.signOut().then(() => window.location.reload());
});
