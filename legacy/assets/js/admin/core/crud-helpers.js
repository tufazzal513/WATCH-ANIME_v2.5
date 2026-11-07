// CRUD helper (unchanged, but we handle users separately)
const setupCrud = (entity, formId, listContainerId, renderListFn, getFormDataFn) => {
    const form = document.getElementById(formId);
    form.addEventListener('submit', (e) => {
        e.preventDefault();
        const formData = getFormDataFn();
        const editId = form.querySelector('input[type=hidden]')?.value;

        if(entity === 'movies') {
            return; 
        }

        if (editId) {
            db.ref(`${entity}/${editId}`).update(formData).then(() => {
                form.reset();
                alert('Updated successfully!');
            });
        } else {
            db.ref(entity).push(formData).then(() => {
                form.reset();
                alert('Added successfully!');
            });
        }
    });
    db.ref(entity).on('value', snapshot => {
        const data = snapshot.val() || {};
        renderListFn(listContainerId, data);
    });
};

window.deleteItem = (entity, id) => {
    if (confirm('Are you sure you want to delete this item?')) {
        db.ref(`${entity}/${id}`).remove();
    }
};

// ========== TOGGLE CONTENT TYPE (unchanged) ==========
window.toggleContentType = function() {
    const type = document.querySelector('input[name="content-type"]:checked').value;
    const singleMovieLinks = document.getElementById('single-movie-links');
    const seriesManager = document.getElementById('series-manager');
    if (type === 'movie') {
        singleMovieLinks.classList.remove('hidden');
        seriesManager.classList.add('hidden');
    } else {
        singleMovieLinks.classList.add('hidden');
        seriesManager.classList.remove('hidden');
    }
};
