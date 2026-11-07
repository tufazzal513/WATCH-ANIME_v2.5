// --- LOGIN / SIGN-UP / FORGOT PASSWORD / LOGOUT ---
const forgotForm = document.getElementById('forgot-form');
const authExtraElements = document.getElementById('auth-extra-elements');

// পাসওয়ার্ড Show/Hide করার লজিক
window.togglePasswordVisibility = function(inputId, btn) {
    const input = document.getElementById(inputId);
    const icon = btn.querySelector('i');
    if (input.type === 'password') {
        input.type = 'text';
        icon.classList.remove('fa-eye');
        icon.classList.add('fa-eye-slash');
    } else {
        input.type = 'password';
        icon.classList.remove('fa-eye-slash');
        icon.classList.add('fa-eye');
    }
};

window.toggleAuthMode = (mode) => {
    authError.textContent = '';
    authError.className = "text-red-400 text-xs text-center mt-4 min-h-[20px]";

    loginForm.classList.add('hidden');
    signupForm.classList.add('hidden');
    forgotForm.classList.add('hidden');

    if (mode === 'signup') {
        signupForm.classList.remove('hidden');
        authExtraElements.classList.remove('hidden');
        document.getElementById('toggle-text-login').classList.add('hidden');
        document.getElementById('toggle-text-signup').classList.remove('hidden');
    } else if (mode === 'forgot') {
        forgotForm.classList.remove('hidden');
        authExtraElements.classList.add('hidden');
    } else {
        // login
        loginForm.classList.remove('hidden');
        authExtraElements.classList.remove('hidden');
        document.getElementById('toggle-text-signup').classList.add('hidden');
        document.getElementById('toggle-text-login').classList.remove('hidden');
    }
};

signupForm.addEventListener('submit', (e) => {
    e.preventDefault();
    auth.createUserWithEmailAndPassword(document.getElementById('signup-email').value, document.getElementById('signup-password').value)
        .then((u) => u.user.updateProfile({ displayName: document.getElementById('signup-name').value }))
        .catch((e) => authError.textContent = e.message.replace("Firebase: ", ""));
});

loginForm.addEventListener('submit', (e) => {
    e.preventDefault();
    auth.signInWithEmailAndPassword(document.getElementById('login-email').value, document.getElementById('login-password').value)
        .catch((e) => authError.textContent = e.message.replace("Firebase: ", ""));
});

// Advanced Forgot Password Logic (Limit, Verification, Cooldown)
forgotForm.addEventListener('submit', async (e) => {
    e.preventDefault();

    if (forgetCooldown > 0) {
        authError.className = "text-amber-400 text-xs text-center mt-4 min-h-[20px]";
        authError.textContent = `Please wait ${forgetCooldown} seconds before trying again.`;
        return;
    }

    const emailInput = document.getElementById('forgot-email');
    const email = emailInput.value.trim();
    const btn = document.getElementById('forgot-submit-btn');

    if(!email) return;

    const originalBtnHtml = btn.innerHTML;
    btn.innerHTML = '<i class="fas fa-spinner fa-spin"></i> Processing...';
    btn.disabled = true;
    authError.textContent = '';

    try {
        // 1. Check 24-hour Limit in Firebase RTDB First
        const emailKey = email.replace(/[\.\#\$\[\]]/g, '_');
        const now = Date.now();
        const oneDayMs = 24 * 60 * 60 * 1000;

        // Fetch latest limit directly from database (Because user is currently logged out)
        const limitSnap = await db.ref('settings/pwdResetLimit').once('value');
        const adminLimit = parseInt(limitSnap.val()) || 3; // Default 3 if not set yet

        const ref = db.ref(`pwd_resets/${emailKey}`);
        const snap = await ref.once('value');
        const data = snap.val() || {};

        let validCount = 0;
        const updates = {};

        Object.entries(data).forEach(([key, ts]) => {
            if(now - ts > oneDayMs) {
                updates[key] = null; // Clean up old records automatically
            } else {
                validCount++;
            }
        });

        if(validCount >= adminLimit) {
            throw new Error(`Limit reached. You can only request a password reset ${adminLimit} times in 24 hours.`);
        }

        // 2. Send Reset Email via Firebase
        // Firebase will automatically handle checking if the account exists
        await auth.sendPasswordResetEmail(email);

        // 3. Update Database Limit Count (Only saves if email was successfully sent)
        updates[ref.push().key] = now;
        await ref.update(updates);

        // Success UI Update
        authError.className = "text-emerald-400 text-xs text-center mt-4 min-h-[20px]";
        authError.textContent = "Reset link sent! Please check your email inbox & spam folder.";
        emailInput.value = '';

        // 4. Start 30-Second Cooldown Timer
        forgetCooldown = 30;
        btn.innerHTML = `<i class="fas fa-clock"></i> Wait ${forgetCooldown}s`;

        const cooldownTimer = setInterval(() => {
            forgetCooldown--;
            if(forgetCooldown <= 0) {
                clearInterval(cooldownTimer);
                btn.innerHTML = originalBtnHtml;
                btn.disabled = false;
                if(authError.textContent.includes("Reset link sent")) authError.textContent = '';
            } else {
                btn.innerHTML = `<i class="fas fa-clock"></i> Wait ${forgetCooldown}s`;
            }
        }, 1000);

    } catch(error) {
        btn.innerHTML = originalBtnHtml;
        btn.disabled = false;
        authError.className = "text-red-400 text-xs text-center mt-4 min-h-[20px]";
        authError.textContent = error.message.replace("Firebase: ", "");
    }
});



window.handleLogout = () => {
    auth.signOut();
    localStorage.removeItem('last_page_id');
    localStorage.removeItem('last_movie_id');
};
