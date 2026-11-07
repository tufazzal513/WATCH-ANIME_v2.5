// --- USER GROWTH CHART LOGIC ---
let userGrowthChart = null;
let currentUserChartFilter = '30';
let cachedUsersForChart = {};

function renderUserGrowthChart() {
    const ctx = document.getElementById('user-growth-chart');
    if(!ctx) return;

    if (!userGrowthChart) {
        userGrowthChart = new Chart(ctx, {
            type: 'line',
            data: { 
                labels: [], 
                datasets: [{ 
                    label: 'New Users', 
                    data: [], 
                    tension: 0.4, 
                    fill: true, 
                    backgroundColor: 'rgba(16, 185, 129, 0.05)', 
                    borderWidth: 2.5, 
                    pointRadius: 1, 
                    pointBackgroundColor: '#1f2937', 
                    pointHoverRadius: 6,
                    segment: {
                        borderColor: ctx => {
                            if(!ctx.p0 || !ctx.p1) return '#10b981';
                            return ctx.p0.parsed.y <= ctx.p1.parsed.y ? '#10b981' : '#ef4444'; 
                        }
                    }
                }] 
            },
            options: { 
                responsive: true, maintainAspectRatio: false, 
                plugins: { legend: { display: false }, tooltip: { mode: 'index', intersect: false } }, 
                scales: { 
                    y: { beginAtZero: true, ticks: { color: '#9ca3af', precision: 0 }, grid: { color: '#374151', borderDash: [5, 5] } }, 
                    x: { ticks: { color: '#9ca3af', maxTicksLimit: 12 }, grid: { display: false } } 
                }
            }
        });

        document.getElementById('user-chart-filter')?.addEventListener('change', (e) => {
            currentUserChartFilter = e.target.value;
            renderUserGrowthChart();
        });
    }

    const makeDayKey = (d) => {
        const y = d.getFullYear(); const m = String(d.getMonth()+1).padStart(2,'0'); const da = String(d.getDate()).padStart(2,'0');
        return `${y}-${m}-${da}`;
    };

    const days = [];
    const labels = [];
    let numDays = parseInt(currentUserChartFilter);

    if (currentUserChartFilter === 'all') {
        let oldest = Date.now();
        Object.values(cachedUsersForChart).forEach(u => {
            if (u.createdAt && u.createdAt < oldest) oldest = u.createdAt;
        });
        numDays = Math.ceil((Date.now() - oldest) / (1000 * 60 * 60 * 24)) + 1;
        if (numDays < 7) numDays = 7;
    }

    for(let i = numDays - 1; i >= 0; i--){
        const d = new Date(); 
        d.setHours(0,0,0,0); 
        d.setDate(d.getDate() - i);
        days.push(makeDayKey(d));

        if (numDays > 365) labels.push(d.toLocaleDateString('en-US', { month: 'short', year: '2-digit' }));
        else if (numDays > 30) labels.push(d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }));
        else labels.push(d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }));
    }

    const usersByDay = Object.fromEntries(days.map(k => [k, 0]));

    Object.values(cachedUsersForChart).forEach(u => {
        if(u && u.status !== 'deleted' && u.createdAt) {
            const d = new Date(u.createdAt);
            d.setHours(0,0,0,0);
            const key = makeDayKey(d);
            if(key in usersByDay) usersByDay[key] += 1;
        }
    });

    userGrowthChart.data.labels = labels;
    userGrowthChart.data.datasets[0].data = days.map(k => usersByDay[k] || 0);
    userGrowthChart.update();
}
