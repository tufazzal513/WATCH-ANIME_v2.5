package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"movex/internal/repo"
)

type dashboardData struct {
	Movies, Users, Active24, Live             int
	Views                                     int64
	ResetTotal, Reset24, OpenBroken, Errors24 int
	TopSearches                               []repo.SearchStat
	Growth                                    []repo.DayCount
	GrowthDays                                int
	GrowthPoints                              string
	GrowthArea                                string
	GrowthMax                                 int
	RecentErrors                              []repo.ErrorLog
	RecentBroken                              []repo.BrokenLink
	RecentAudit                               []repo.AuditEntry
}

func (a *Admin) dashboard(c *Ctx) {
	ctx := c.R.Context()
	now := a.now()
	days := 30
	if n, err := strconv.Atoi(c.R.URL.Query().Get("days")); err == nil && (n == 7 || n == 30 || n == 90) {
		days = n
	}
	d := dashboardData{GrowthDays: days}
	var err error
	if d.Movies, err = a.deps.Repo.CountMovies(ctx); err != nil {
		a.logQuery("dashboard movies", err)
	}
	if users, e := a.deps.Repo.CountUsers(ctx); e != nil {
		a.logQuery("dashboard users", e)
	} else {
		d.Users = users.Total
	}
	if d.Active24, err = a.deps.Repo.CountActiveUsersSince(ctx, now.Add(-24*time.Hour)); err != nil {
		a.logQuery("dashboard active users", err)
	}
	if a.deps.Presence != nil {
		d.Live = a.deps.Presence.CountLive()
	}
	if views, e := a.deps.Repo.TotalPlays(ctx); e != nil {
		a.logQuery("dashboard views", e)
	} else {
		d.Views = views
	}
	if d.ResetTotal, d.Reset24, err = a.deps.Repo.ResetStats(ctx); err != nil {
		a.logQuery("dashboard resets", err)
	}
	if d.OpenBroken, err = a.deps.Repo.CountOpenBrokenLinks(ctx); err != nil {
		a.logQuery("dashboard broken links", err)
	}
	if d.Errors24, err = a.deps.Repo.CountErrorsSince(ctx, now.Add(-24*time.Hour)); err != nil {
		a.logQuery("dashboard errors", err)
	}
	if d.TopSearches, err = a.deps.Repo.TopSearches(ctx, 10); err != nil {
		a.logQuery("dashboard searches", err)
	}
	if d.Growth, err = a.deps.Repo.UserGrowth(ctx, days); err != nil {
		a.logQuery("dashboard growth", err)
	}
	d.GrowthPoints, d.GrowthArea, d.GrowthMax = growthSVG(d.Growth)
	if d.RecentErrors, err = a.deps.Repo.RecentErrors(ctx, 6); err != nil {
		a.logQuery("dashboard recent errors", err)
	}
	if d.RecentBroken, _, err = a.deps.Repo.ListBrokenLinks(ctx, repo.BrokenListOpts{Status: "open", Limit: 6}); err != nil {
		a.logQuery("dashboard recent broken links", err)
	}
	if d.RecentAudit, err = a.deps.Repo.RecentAudit(ctx, 8); err != nil {
		a.logQuery("dashboard recent audit", err)
	}
	c.Render("dashboard", Page{Title: "Dashboard", Active: "dashboard", Data: d})
}

func growthSVG(days []repo.DayCount) (points, area string, maxValue int) {
	if len(days) == 0 {
		return "", "", 1
	}
	for _, d := range days {
		if d.Count > maxValue {
			maxValue = d.Count
		}
	}
	if maxValue < 1 {
		maxValue = 1
	}
	const width, height, padX, padY = 720.0, 180.0, 8.0, 12.0
	coords := make([]string, 0, len(days))
	for i, d := range days {
		x := padX
		if len(days) > 1 {
			x += float64(i) * (width - 2*padX) / float64(len(days)-1)
		}
		y := height - padY - float64(d.Count)/float64(maxValue)*(height-2*padY)
		coords = append(coords, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	points = joinPoints(coords)
	area = fmt.Sprintf("%s %.1f,%.1f %.1f,%.1f", points, width-padX, height-padY, padX, height-padY)
	return points, area, maxValue
}

func joinPoints(p []string) string {
	out := ""
	for i, v := range p {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}

func (a *Admin) logQuery(where string, err error) {
	if err != nil {
		a.deps.Log.Warn(where+" query failed", "err", err)
	}
}

func (a *Admin) brokenLinks(c *Ctx) {
	q := c.R.URL.Query().Get("q")
	kind := c.R.URL.Query().Get("type")
	status := c.R.URL.Query().Get("status")
	if kind != "" && kind != "movie" && kind != "series" {
		kind = ""
	}
	if status != "" && status != "open" && status != "resolved" {
		status = ""
	}
	page, _ := strconv.Atoi(c.R.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const pageSize = 25
	items, total, err := a.deps.Repo.ListBrokenLinks(c.R.Context(), repo.BrokenListOpts{
		Query: q, ContentType: kind, Status: status, Offset: (page - 1) * pageSize, Limit: pageSize,
	})
	if err != nil {
		c.Error(http.StatusInternalServerError, "Could not load broken link reports")
		return
	}
	data := brokenLinksData{Items: items, Total: total, Page: page, Pages: max(1, (total+pageSize-1)/pageSize), Query: q, Type: kind, Status: status, CSRF: c.Session.CSRF}
	if c.IsHTMX() {
		c.RenderBlock("broken-links", "broken-table", data)
		return
	}
	c.Render("broken-links", Page{Title: "Broken Links", Active: "broken-links", Data: data})
}

type brokenLinksData struct {
	Items               []repo.BrokenLink
	Total, Page, Pages  int
	Query, Type, Status string
	CSRF                string
}

func (a *Admin) resolveBrokenLink(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.ResolveBrokenLink(c.R.Context(), id); err != nil {
		if err == repo.ErrNotFound {
			c.Error(http.StatusNotFound, "Report not found")
		} else {
			c.Error(http.StatusInternalServerError, "Could not resolve report")
		}
		return
	}
	c.Audit("broken-link.resolve", id, "Resolved broken link report")
	c.Flash("success", "Broken link report marked as resolved.")
	c.Redirect("/admin/broken-links")
}

func (a *Admin) deleteBrokenLink(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteBrokenLink(c.R.Context(), id); err != nil {
		if err == repo.ErrNotFound {
			c.Error(http.StatusNotFound, "Report not found")
		} else {
			c.Error(http.StatusInternalServerError, "Could not delete report")
		}
		return
	}
	c.Audit("broken-link.delete", id, "Deleted broken link report")
	c.Flash("success", "Broken link report deleted.")
	c.Redirect("/admin/broken-links")
}
