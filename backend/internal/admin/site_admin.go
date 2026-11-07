package admin

import (
	"strconv"
	"strings"

	"movex/internal/repo"
)

func (a *Admin) categoriesPage(c *Ctx) {
	items, err := a.deps.Repo.ListCategories(c.R.Context())
	if err != nil {
		c.Error(500, "Could not load categories")
		return
	}
	c.Render("categories", Page{Title: "Categories", Active: "categories", Data: items})
}

func (a *Admin) saveCategory(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	name := strings.TrimSpace(c.R.FormValue("name"))
	popular := c.R.FormValue("popular") == "on"
	sort, _ := strconv.Atoi(c.R.FormValue("sort"))
	id := c.R.PathValue("id")
	var item *repo.Category
	var err error
	if id == "" {
		item, err = a.deps.Repo.CreateCategory(c.R.Context(), name, popular, sort)
	} else {
		item, err = a.deps.Repo.UpdateCategory(c.R.Context(), id, name, popular, sort)
	}
	if err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/categories")
		return
	}
	action := "category.create"
	if id != "" {
		action = "category.update"
	}
	c.Audit(action, item.ID, "Saved category: "+item.Name)
	c.Flash("success", "Category saved.")
	c.Redirect("/admin/categories")
}

func (a *Admin) deleteCategory(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteCategory(c.R.Context(), id); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/categories")
		return
	}
	c.Audit("category.delete", id, "Deleted category")
	c.Flash("success", "Category deleted.")
	c.Redirect("/admin/categories")
}

func (a *Admin) reorderCategories(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	ids := strings.Split(strings.TrimSpace(c.R.FormValue("order")), ",")
	if len(ids) > 500 {
		c.Error(400, "Too many categories")
		return
	}
	if err := a.deps.Repo.ReorderCategories(c.R.Context(), ids); err != nil {
		c.Error(500, "Could not reorder categories")
		return
	}
	c.Audit("category.reorder", "", "Updated category order")
	c.Flash("success", "Category order updated.")
	c.Redirect("/admin/categories")
}

func (a *Admin) bannersPage(c *Ctx) {
	items, err := a.deps.Repo.ListBanners(c.R.Context(), false)
	if err != nil {
		c.Error(500, "Could not load banners")
		return
	}
	c.Render("banners", Page{Title: "Banners", Active: "banners", Data: items})
}

func (a *Admin) saveBanner(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	sort, _ := strconv.Atoi(c.R.FormValue("sort"))
	in := repo.BannerInput{ImageURL: strings.TrimSpace(c.R.FormValue("image_url")), MovieID: strings.TrimSpace(c.R.FormValue("movie_id")), LinkURL: strings.TrimSpace(c.R.FormValue("link_url")), Sort: sort, Active: c.R.FormValue("active") == "on"}
	id := c.R.PathValue("id")
	var item *repo.Banner
	var err error
	if id == "" {
		item, err = a.deps.Repo.CreateBanner(c.R.Context(), in)
	} else {
		item, err = a.deps.Repo.UpdateBanner(c.R.Context(), id, in)
	}
	if err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/banners")
		return
	}
	action := "banner.create"
	if id != "" {
		action = "banner.update"
	}
	c.Audit(action, item.ID, "Saved banner")
	c.Flash("success", "Banner saved.")
	c.Redirect("/admin/banners")
}

func (a *Admin) deleteBanner(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteBanner(c.R.Context(), id); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/banners")
		return
	}
	c.Audit("banner.delete", id, "Deleted banner")
	c.Flash("success", "Banner deleted.")
	c.Redirect("/admin/banners")
}

func (a *Admin) reorderBanners(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	ids := strings.Split(strings.TrimSpace(c.R.FormValue("order")), ",")
	if len(ids) > 500 {
		c.Error(400, "Too many banners")
		return
	}
	if err := a.deps.Repo.ReorderBanners(c.R.Context(), ids); err != nil {
		c.Error(500, "Could not reorder banners")
		return
	}
	c.Audit("banner.reorder", "", "Updated banner order")
	c.Flash("success", "Banner order updated.")
	c.Redirect("/admin/banners")
}

type usersPageData struct {
	Users         []*repo.User
	Total         int
	Query, Status string
}

func (a *Admin) usersPage(c *Ctx) {
	q := c.R.URL.Query()
	status := q.Get("status")
	if status == "all" {
		status = ""
	}
	items, total, err := a.deps.Repo.ListUsers(c.R.Context(), repo.UserListOpts{Query: strings.TrimSpace(q.Get("q")), Status: status, Limit: 50})
	if err != nil {
		c.Error(400, "Invalid user filter")
		return
	}
	c.Render("users", Page{Title: "Users", Active: "users", Data: usersPageData{Users: items, Total: total, Query: q.Get("q"), Status: status}})
}

func (a *Admin) setUserStatus(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	uid := c.R.PathValue("uid")
	status := c.R.FormValue("status")
	if uid == c.UID() && status != repo.StatusActive {
		c.Error(400, "You cannot block or delete your own admin account")
		return
	}
	if err := a.deps.Repo.SetUserStatus(c.R.Context(), uid, status); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/users")
		return
	}
	c.Audit("user.status", uid, "Changed user status to "+status)
	c.Flash("success", "User status updated.")
	c.Redirect("/admin/users")
}

// ---------- settings, social links and notifications ----------

type settingsPageData struct {
	Settings      map[string]string
	Socials       []repo.SocialLink
	Notifications []repo.Notification
}

func (a *Admin) settingsPage(c *Ctx) {
	settings, err := a.deps.Repo.GetSettings(c.R.Context())
	if err != nil {
		c.Error(500, "Could not load settings")
		return
	}
	if settings["appName"] == "" {
		settings["appName"] = "MoveX"
	}
	if settings["themeMode"] == "" {
		settings["themeMode"] = "static"
	}
	if settings["themeColor"] == "" {
		settings["themeColor"] = "#e50914"
	}
	if settings["themeInterval"] == "" {
		settings["themeInterval"] = "60"
	}
	if settings["pwdResetLimit"] == "" {
		settings["pwdResetLimit"] = "3"
	}
	socials, err := a.deps.Repo.ListSocialLinks(c.R.Context())
	if err != nil {
		c.Error(500, "Could not load social links")
		return
	}
	notifications, err := a.deps.Repo.ListNotifications(c.R.Context(), 100)
	if err != nil {
		c.Error(500, "Could not load notifications")
		return
	}
	c.Render("settings", Page{Title: "Settings", Active: "settings", Data: settingsPageData{Settings: settings, Socials: socials, Notifications: notifications}})
}

func (a *Admin) saveSettings(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	keys := []string{"appName", "appLogo", "themeMode", "themeColor", "themeInterval", "pwdResetLimit"}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = strings.TrimSpace(c.R.FormValue(key))
	}
	if err := a.deps.Repo.SetSettings(c.R.Context(), values); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/settings")
		return
	}
	c.Audit("settings.update", "", "Updated public app settings and password reset limit")
	c.Flash("success", "Settings saved.")
	c.Redirect("/admin/settings")
}

func (a *Admin) saveSocial(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	in := repo.SocialInput{Name: c.R.FormValue("name"), URL: c.R.FormValue("url"), Icon: c.R.FormValue("icon")}
	id := c.R.PathValue("id")
	var item *repo.SocialLink
	var err error
	if id == "" {
		item, err = a.deps.Repo.CreateSocialLink(c.R.Context(), in)
	} else {
		item, err = a.deps.Repo.UpdateSocialLink(c.R.Context(), id, in)
	}
	if err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/settings")
		return
	}
	action := "social.create"
	if id != "" {
		action = "social.update"
	}
	c.Audit(action, item.ID, "Saved social link: "+item.Name)
	c.Flash("success", "Social link saved.")
	c.Redirect("/admin/settings")
}

func (a *Admin) deleteSocial(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteSocialLink(c.R.Context(), id); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/settings")
		return
	}
	c.Audit("social.delete", id, "Deleted social link")
	c.Flash("success", "Social link deleted.")
	c.Redirect("/admin/settings")
}

func (a *Admin) saveNotification(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	id := c.R.PathValue("id")
	var item *repo.Notification
	var err error
	if id == "" {
		item, err = a.deps.Repo.CreateNotification(c.R.Context(), c.R.FormValue("title"), c.R.FormValue("message"))
	} else {
		item, err = a.deps.Repo.UpdateNotification(c.R.Context(), id, c.R.FormValue("title"), c.R.FormValue("message"))
	}
	if err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/settings")
		return
	}
	action := "notification.send"
	if id != "" {
		action = "notification.update"
	}
	c.Audit(action, item.ID, "Saved notification: "+item.Title)
	c.Flash("success", "Notification saved.")
	c.Redirect("/admin/settings")
}

func (a *Admin) deleteNotification(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteNotification(c.R.Context(), id); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/settings")
		return
	}
	c.Audit("notification.delete", id, "Deleted notification")
	c.Flash("success", "Notification deleted.")
	c.Redirect("/admin/settings")
}
