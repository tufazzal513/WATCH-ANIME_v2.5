// --- USER RECORD + STATUS CHECK ---
async function ensureUserRecord(user) {
    if(!user || !user.uid) return;
    const ref = db.ref('users/' + user.uid);
    const snap = await ref.once('value');
    const existingData = snap.val();

    // যদি ডেটাবেজে কোনো রেকর্ড না থাকে, তার মানে নতুন একাউন্ট খুলেছে
    if (!existingData) {
        const data = {
            uid: user.uid,
            name: user.displayName || "User",
            email: user.email || "",
            photoURL: user.photoURL || "",
            status: "active", 
            createdAt: firebase.database.ServerValue.TIMESTAMP,
            lastLoginAt: firebase.database.ServerValue.TIMESTAMP
        };
        await ref.set(data); 
        return "active";
    }

    const currentStatus = existingData.status || "active";

    // যদি একাউন্ট ডিলিট বা ফ্রিজ না থাকে, তাহলে শুধু লাস্ট লগিন টাইম আপডেট হবে
    if (currentStatus !== "deleted" && currentStatus !== "frozen") {
        await ref.update({ lastLoginAt: firebase.database.ServerValue.TIMESTAMP });
    }

    return currentStatus; 
}

let userStatusRef = null;

// ফ্রিজ/ডিলিট চেক এবং লগআউট করার ফাংশন (Real-time)
function showBannedPopup(status) {
    const msg = (status === "frozen")
        ? "Your account is frozen by Admin. You cannot access the app."
        : "Your account was permanently deleted. You have been logged out.";

    const existing = document.querySelectorAll('.frozen-popup');
    existing.forEach(el => el.remove());

    const popup = document.createElement('div');
    popup.className = 'frozen-popup';
    popup.innerText = msg;
    document.body.appendChild(popup);
    setTimeout(() => popup.remove(), 8000);
}
