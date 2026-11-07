package admin

import (
	"strings"
	"time"

	"movex/internal/repo"
)

type workersPageData struct {
	Workers   []repo.Worker
	NewKey    string
	NewWorker *repo.Worker
	Now       int64
}

func (a *Admin) workersPage(c *Ctx) {
	items, err := a.deps.Repo.ListWorkers(c.R.Context())
	if err != nil {
		c.Error(500, "Could not load workers")
		return
	}
	c.Render("workers", Page{Title: "Workers", Active: "workers", Data: workersPageData{Workers: items, Now: time.Now().UnixMilli()}})
}

func (a *Admin) createWorker(c *Ctx) {
	if err := c.R.ParseForm(); err != nil {
		c.Error(400, "Invalid form")
		return
	}
	name := strings.TrimSpace(c.R.FormValue("name"))
	url := strings.TrimRight(strings.TrimSpace(c.R.FormValue("url")), "/")
	worker, key, err := a.deps.Repo.CreateWorker(c.R.Context(), name, url)
	if err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/workers")
		return
	}
	c.Audit("worker.add", worker.ID, "Registered worker: "+worker.Name)
	items, err := a.deps.Repo.ListWorkers(c.R.Context())
	if err != nil {
		c.Error(500, "Worker was created but the list could not be loaded")
		return
	}
	c.Render("workers", Page{Title: "Workers", Active: "workers", Data: workersPageData{Workers: items, NewKey: key, NewWorker: worker, Now: time.Now().UnixMilli()}})
}

func (a *Admin) deleteWorker(c *Ctx) {
	id := c.R.PathValue("id")
	if err := a.deps.Repo.DeleteWorker(c.R.Context(), id); err != nil {
		c.Flash("error", err.Error())
		c.Redirect("/admin/workers")
		return
	}
	c.Audit("worker.delete", id, "Deleted worker")
	c.Flash("success", "Worker removed.")
	c.Redirect("/admin/workers")
}
