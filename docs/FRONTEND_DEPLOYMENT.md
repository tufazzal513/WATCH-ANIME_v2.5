# MoveX frontend deployment (GitHub Pages)

1. Push the repository to GitHub and select **Settings → Pages → Build and deployment → GitHub Actions**.
2. Under **Settings → Secrets and variables → Actions → Variables**, add `MOVEX_API_BASE` with the HTTPS origin of the Go main server, for example `https://api.example.com` (no trailing slash or path).
3. Configure the Go main server's `ALLOWED_ORIGINS` to include the exact GitHub Pages origin, for example `https://OWNER.github.io`. If the site is published under a project path, the origin still has no repository path.
4. Ensure Firebase Authentication's authorized domains include the GitHub Pages hostname.
5. Push changes under `frontend/` or manually run the **Deploy MoveX frontend to GitHub Pages** workflow.

The API base is public configuration, not a secret. Do not put Firebase service-account credentials, Telegram API hashes, worker keys, or session secrets in frontend files or GitHub Pages variables.

The current deployment workflow publishes the static frontend. It does not deploy the Go backend. The frontend migration is still in progress: the legacy compatibility facade must be removed from active call sites before the site can be considered production-ready against the Go API.

## Main/worker replication operations

- Add each worker in the main admin panel and copy its per-worker `WORKER_KEY` into that worker's environment. The main database stores only a one-way hash of this key.
- For immediate worker wake-ups, generate a separate random secret of at least 32 characters and set it as `SYNC_NOTIFY_KEY` on the main server and every worker. Never reuse a worker's `WORKER_KEY` as this shared notification secret.
- `SYNC_NOTIFY_KEY` is optional. Failed wake-up deliveries are retained by the main server and retried with bounded exponential backoff (up to one minute). If the key is absent or delivery remains unavailable, workers continue using `SYNC_EVERY_SEC` polling.
- Worker progress is recorded only after the worker commits a replication batch and posts an authenticated acknowledgement to the main server. Merely downloading a batch does not mark the worker as synchronized.
- Acknowledgements are tied to the current replication epoch. After a main database restore/reset changes the epoch, old worker acknowledgements no longer make that worker eligible for public server discovery until it applies and acknowledges a fresh snapshot.
