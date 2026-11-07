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
        // The Go backend owns per-IP/per-email limits and sends the Firebase reset email.
        const result = await MX_API.passwordReset(email);
        authError.className = "text-emerald-400 text-xs text-center mt-4 min-h-[20px]";
        authError.textContent = (result && result.message) || "If an account exists for this email, a reset link has been sent. Check your inbox and spam folder.";
        emailInput.value = '';
        forgetCooldown = 30;
        btn.innerHTML = `<i class="fas fa-clock"></i> Wait ${forgetCooldown}s`;
        const cooldownTimer = setInterval(() => {
            forgetCooldown--;
            if (forgetCooldown <= 0) {
                clearInterval(cooldownTimer);
                btn.innerHTML = originalBtnHtml;
                btn.disabled = false;
                if (authError.textContent.includes('reset link')) authError.textContent = '';
            } else btn.innerHTML = `<i class="fas fa-clock"></i> Wait ${forgetCooldown}s`;
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
