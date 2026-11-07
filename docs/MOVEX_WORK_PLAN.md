# MoveX Go Migration: ২০ ভাগে কাজের পরিকল্পনা

> উদ্দেশ্য: প্রতিটি ভাগ একটা টোকেন-লিমিটের ভেতরে শেষ হবে। লিমিট রিসেটের পর পরের ভাগ।
> এই ফাইল `MOVEX_GO_MIGRATION_PROMPT.md` এর **সহায়ক**। সিদ্ধান্ত ও স্পেক ওই প্রম্পটেই; এখানে শুধু কাজ ভাগ ও চালানোর নিয়ম।

---

## ১. বিশ্লেষণের ফল (প্রজেক্টের আসল আকার)

| এলাকা | ফাইল | আকার | নোট |
|---|---|---|---|
| পুরনো সার্ভার (Node) | ১১টি | ~৭১ KB | পোর্ট করতে হবে |
| সার্ভার টেস্ট | ২টি | ~২২ KB | Go-তে পোর্ট |
| অ্যাডমিন JS | ২২টি | ~৯২ KB | সবচেয়ে বড়: `users.js` ১২KB, `movie-form.js` ৯.৪KB |
| অ্যাডমিন HTML | ১২টি | ~৮০ KB | `movies.html` ১৮KB, `dashboard.html` ১৭KB |
| ইউজার JS | ২৭টি | ~১০৪ KB | `watch-player.js` একাই ৩৩KB |
| শেয়ার্ড JS | ৩টি | ~২২ KB | `db-client.js` ১৮KB (পুরো বদলাবে) |
| ইউজার HTML | ১৩টি | ~২৮ KB | |
| CSS | ২৪টি | ~৫৬ KB | **পড়ার দরকার নেই**, শুধু কপি/রিইউজ |
| প্রম্পট | ১টি | ~৪৫ KB | প্রতি সেশনে পুরোটা পড়বে না (নিচে দেখো) |

**ফলাফল:** পুরনো কোড ছোট, কিন্তু নতুন Go কোড লিখতে হবে অনেক বেশি (অ্যাডমিন SSR, টেলিগ্রাম, রেপ্লিকেশন, টেস্ট)। তাই ভাগ করা হয়েছে "পড়ার পরিমাণ + লেখার পরিমাণ" মিলিয়ে।

## ২. ⚠️ শুরুর আগে একটা পরিবেশ-সমস্যা

এই Claude চ্যাট সেশনে **Go ইনস্টল নেই** এবং `proxy.golang.org`, `gitlab.com` (modernc.org/sqlite এর আসল হোস্ট) **ব্লক**। ফলে এখানে `go build`/`go test` চলবে না।

সমাধান (যেকোনো একটা):
- **ক) Claude Code (ডেস্কটপ/টার্মিনাল)** ব্যবহার করো; নিজের মেশিনে Go থাকলে সবচেয়ে সহজ।
- **খ)** Claude সেটিংসে নেটওয়ার্ক অনুমতি বাড়াও: `proxy.golang.org`, `sum.golang.org`, `gitlab.com`, `go.dev`, `dl.google.com`, `storage.googleapis.com`।

**P1-এর প্রথম কাজই এটা যাচাই করা।** না চললে কোড লেখা চলবে, কিন্তু টেস্ট চালানো যাবে না; সেটা `PROGRESS.md`-তে লিখে রাখবে।

---

## ৩. প্রতিটি সেশনের নিয়ম (সবচেয়ে গুরুত্বপূর্ণ)

### সেশন শুরুতে অ্যাটাচ করো
1. **সর্বশেষ রিপো zip** (প্রথমবার: মূল `WAnime-front-back_Aparthost_v2_3.zip`)
2. `MOVEX_GO_MIGRATION_PROMPT.md`
3. এই `MOVEX_WORK_PLAN.md`

### কিক-অফ মেসেজ (কপি-পেস্ট)
```
MOVEX_WORK_PLAN.md ও MOVEX_GO_MIGRATION_PROMPT.md পড়ো।
(PROGRESS.md থাকলে সেটাও পড়ো।)
আজ শুধু PART <N> করবে, এর বাইরে কিছু শুরু করবে না।
প্রম্পটের পুরোটা নয়, শুধু §০, §২ এবং প্ল্যানে ওই পার্টের জন্য লেখা § গুলো পড়বে।
প্রতিটি উপ-ধাপ শেষে PROGRESS.md আপডেট করবে।
পার্ট শেষে: টেস্ট চালাও, PROGRESS.md আপডেট করো, পুরো রিপো zip করে দাও, তারপর থামো।
```

### সেশন শেষে (অবশ্যই)
- `PROGRESS.md` এ লেখা: ✅ কী হলো, ⏭️ পরের পার্ট, 🧠 নেওয়া সিদ্ধান্ত, ⚠️ সমস্যা
- **পুরো রিপো `movex-after-partN.zip` হিসেবে ডাউনলোড করে রাখো**; পরের সেশনে এটাই অ্যাটাচ করবে। ফাইল সিস্টেম প্রতি টাস্কে রিসেট হয়, zip না নিলে কাজ হারাবে।

### লিমিট মাঝপথে শেষ হলে
- কাজ প্রতিটি উপ-ধাপের পর `PROGRESS.md`-তে চেকপয়েন্ট রাখে বলে ক্ষতি নেই।
- পরের সেশনে একই PART নম্বর দাও; Claude `PROGRESS.md` দেখে যেখানে থেমেছিল সেখান থেকে চালাবে।

### ভাগ মেলানো/ভাঙার নিয়ম
- লিমিট বড় মনে হলে **পরপর দুটো ছোট পার্ট** (যেমন P12+P8-টাইপ হালকাগুলো) একসাথে দিতে পারো।
- কোনো পার্ট লিমিটে না কুলালে সেটাকে **a/b** ভাগ করো (প্রতিটি পার্টে উপ-ধাপ দেওয়া আছে)।

---

## ৪. ২০টি ভাগ

"ভার" = আনুমানিক কাজের ওজন (পড়া + লেখা + টেস্ট), ১ ≈ হালকা, ৪ ≈ ভারী। গড় ≈ ৩.২, কোনো ভাগ ২ এর নিচে বা ৪ এর বেশি নয়।

### ধাপ A: ব্যাকএন্ড কোর (P1 – P6)

**P1: ফাউন্ডেশন** (ভার ২.৫) · প্রম্পট §০ §২ §৩ §৪ §১২(বিল্ড অংশ)
- পড়বে: `server/src/config.js`, `index.js`, `util.js`
- করবে: `frontend/` `backend/` `legacy/` ভাগ (ফাইল সরানো, মেকানিক্যাল); `go.mod`, config প্যাকেজ (env যাচাই), `slog`, graceful HTTP সার্ভার, `/api/v1/health`, মাইগ্রেশন runner, ইন-মেমরি presence, `PROGRESS.md`, `.gitignore`
- **আগে যাচাই:** Go টুলচেইন ও ডিপেন্ডেন্সি ডাউনলোড চলে কিনা (§২)
- টেস্ট: config, মাইগ্রেশন runner (idempotent)

**P2: স্কিমা + ক্যাটালগ রিপো** (ভার ৩.৫) · §৫
- পড়বে: `movie-form.js`, `seasons-episodes.js`, `banners.js`, `classification.js`, `socials-notifications.js`, `taxonomy.js`, `rules.js`, `database.rules.json`
- করবে: `002_catalog.sql` (movies, seasons, episodes, links, categories, banners, social, notifications, settings, FTS5); প্রতিটির repo; **`changes` লগ একই ট্রানজাকশনে** (রেপ্লিকেশনের ভিত্তি)
- টেস্ট: CRUD, ক্যাসকেড ডিলিট, changes লগ

**P3: ব্যক্তিগত ডাটা রিপো** (ভার ২.৫) · §৫(মেইন-অনলি অংশ)
- পড়বে: `store.js`, `rules.js`, `users.js`(অ্যাডমিন), `broken-links.js`, `user-record.js`, `user-lists.js`
- করবে: `003_private.sql` (users, my_list, watch_progress, broken_links, error_logs, search_stats, site_stats, pwd_reset_attempts, audit_log, workers, meta); repo ফাংশন
- টেস্ট: write-once error log, progress ফরম্যাট (`t`,`d`), my_list ভ্যালিডেশন

**P4: Auth ইঞ্জিন + মিডলওয়্যার** (ভার ৩) · §৭.১ §৭.৩
- পড়বে: `server/src/auth.js`, `app.js` (middleware অংশ)
- করবে: Firebase RS256 JWT যাচাই + JWKS ক্যাশ, `DEV_INSECURE_AUTH` গার্ড, মিডলওয়্যার (CORS, gzip, ETag, rate-limit, recover, logging, body limit)
- টেস্ট: fake JWKS সার্ভারে বৈধ/মেয়াদোত্তীর্ণ/ভুল aud/iss/kid/alg/সিগনেচার

**P5: ইউজার API: ক্যাটালগ** (ভার ৩) · §৬ (প্রথমার্ধ)
- পড়বে: `db-client.js`, `search.js`, `home.js`, `categories.js`, `category-movies.js`, `notifications.js`(ইউজার), `app.js`
- করবে: health, servers, settings/public, movies (lite/পেজিনেশন/FTS/ফিল্টার/সর্ট), movies/{id}, categories, banners, social-links, notifications, search suggest/hit, view
- টেস্ট: ETag/304, পেজিনেশন, ব্লকড ইউজার ৪০৩

**P6: ইউজার API: ব্যক্তিগত** (ভার ৩) · §৬ (দ্বিতীয়ার্ধ) §৭
- পড়বে: `rules.js`, `store.js`(ভ্যালিডেশন), `watch-player.js` (শুধু progress অংশ), `broken-link-reporter.js`, `error-logger.js`, `auth-forms.js` (reset অংশ)
- করবে: me, me/list, me/progress, presence, broken-links, errors, auth/password-reset
- টেস্ট: **অথরাইজেশন ম্যাট্রিক্স** (অথ নেই / ইউজার / অন্য ইউজার / ব্লকড / অ্যাডমিন)

### ধাপ B: অ্যাডমিন SSR (P7 – P12)

**P7: অ্যাডমিন ইনফ্রা** (ভার ৩.৫) · §৭.২ §৮ (লেআউট)
- পড়বে: `admin/index.html`, `admin/partials/*`, `admin/core/*.js`, `admin/bootstrap.js`, `css/admin/*`
- করবে: login/logout, Firebase REST লগইন, সাইন করা কুকি, CSRF, rate limit, `base.html` লেআউট (সাইডবার/হেডার/বটম-নেভ), embed স্ট্যাটিক (CSS + htmx), CSP/সিকিউরিটি হেডার, audit helper
- htmx সোর্স: npm রেজিস্ট্রি থেকে (`htmx.org`) রিপোতে রাখবে

**P8: ড্যাশবোর্ড + ব্রোকেন লিংক** (ভার ৩) · §৮
- পড়বে: `dashboard.html`(১৭KB), `dashboard.js`, `user-growth-chart.js`, `broken-links.html`/`.js`
- করবে: `/admin/` কার্ড, সার্চ স্ট্যাটস, **সার্ভার-রেন্ডার SVG গ্রোথ চার্ট**, সাম্প্রতিক এরর; `/admin/broken-links`

**P9: মুভি ফর্ম** (ভার ৩.৫) · §৮
- পড়বে: `movies.html`(১৮KB), `movie-form.js`, `movie-actions.js`, `link-detector.js`, `taxonomy.js`, `crud-helpers.js`
- করবে: `/admin/movies/new` ও `/{id}/edit` (মেটাডেটা, ট্যাক্সোনমি, পোস্টার/ব্যাকড্রপ প্রিভিউ, লিংক-ডিটেক্টর, তৈরি/আপডেট/ডিলিট, ভ্যালিডেশন)

**P10: সিজন/এপিসোড/লিংক + All Movies** (ভার ৩) · §৮
- পড়বে: `seasons-episodes.js`, `all-movies.html`, `movies.html`-এর এপিসোড অংশ, `movie-actions.js` (বাল্ক)
- করবে: সিজন/এপিসোড CRUD + ক্রম, স্ট্রিম/ডাউনলোড লিংক ম্যানেজার, All Movies (সার্চ/ফিল্টার/সর্ট/পেজিনেশন/বাল্ক)

**P11: ইউজার + ক্যাটাগরি** (ভার ৩.৫) · §৮
- পড়বে: `users.html`, `users.js`(১২KB), `classification.js` ও তার HTML
- করবে: `/admin/users` (তালিকা/সার্চ/ফিল্টার/block-unblock-delete/My List ও প্রগ্রেস দেখা/অনলাইন সূচক); `/admin/categories` CRUD + সর্ট

**P12: ব্যানার + সেটিংস** (ভার ৩) · §৮
- পড়বে: `banners.html`/`.js`, `settings.html`(সোশ্যাল/নোটিফিকেশন/পাবলিক অংশ), `socials-notifications.js`, `settings.js`
- করবে: `/admin/banners` CRUD + সর্ট; `/admin/settings` (সোশ্যাল লিংক, নোটিফিকেশন পাঠানো/মুছা, পাবলিক সাইট সেটিংস)
- ⚠️ পুরনো "Server Settings" অংশ **বাদ**, সেটা P14-এ নতুন `/admin/server` হয়ে আসবে

### ধাপ C: অপারেশন (P13 – P16)

**P13: টেলিগ্রাম ব্যাকআপ** (ভার ৩.৫) · §১০ (ব্যাকআপ)
- পড়বে: `telegram.js`, `backup.js`, `config.js`
- করবে: টেলিগ্রাম ক্লায়েন্ট ইন্টারফেস + `gotd/td` বাস্তবায়ন, `VACUUM INTO` স্ন্যাপশট → gzip → আপলোড (ক্যাপশন সহ) → পিন → রিটেনশন, retry/FloodWait, mutex, শিডিউলার
- টেস্ট: মক ক্লায়েন্টে

**P14: রিস্টোর + Telegram Login + `/admin/server` UI** (ভার ৩.৫) · §১০ (রিস্টোর/লগইন) §৮ (`/admin/server`)
- পড়বে: `restore.js`, `scripts/tg-login.js`, `server-config.js`, `server-backup.js`, `settings.html`(সার্ভার অংশ)
- করবে: অটো-রিস্টোর (শুধু খালি DB-তে) + ম্যানুয়াল Restore ("RESTORE" টাইপ কনফার্ম), যাচাই (`integrity_check`), অ্যাটমিক রিপ্লেস, ফোন→কোড→2FA লগইন ফ্লো, Backup now, Test Telegram, env সারসংক্ষেপ (সিক্রেট ছাড়া)
- টেস্ট: খালি DB তে রিস্টোর, ডাটা-ওয়ালা DB তে নয়, অবৈধ ফাইল প্রত্যাখ্যান

**P15: রেপ্লিকেশন: মেইন দিক** (ভার ৩) · §৯ (মেইন)
- পড়বে: `sync.js`, `store.js` (seq/tomb অংশ), `cluster.test.js`
- করবে: `/internal/sync` (+ `epoch`, `max_seq`), `changes` ছাঁটাই, `/internal/notify` ডিবাউন্স, ওয়ার্কার ম্যানেজমেন্ট (নাম+URL → একবার `WORKER_KEY` দেখানো, হ্যাশ সংরক্ষণ), `/api/v1/servers`, অ্যাডমিন ওয়ার্কার UI

**P16: রেপ্লিকেশন: ওয়ার্কার দিক** (ভার ৩.৫) · §৯ (ওয়ার্কার)
- করবে: puller (ইনক্রিমেন্টাল + ফুল রিসিঙ্ক), লোকাল DB, রাউটিং (ক্যাটালগ GET লোকালি; বাকি সব মেইনে প্রক্সি, `X-Forwarded-For` ঠিক রেখে), মেইন ডাউনে লেখায় ৫০৩/পড়া চালু, `ROLE=worker` এ অ্যাডমিন/ব্যাকআপ বন্ধ
- টেস্ট: `httptest` এ মেইন+ওয়ার্কার (ইনক্রিমেন্টাল, ডিলিট, epoch বদলে রিসিঙ্ক, লেখা প্রক্সি)

### ধাপ D: ফ্রন্টএন্ড (P17 – P19)

**P17: ফ্রন্টএন্ড API ক্লায়েন্ট** (ভার ৩) · §১১
- পড়বে: `db-client.js`(১৮KB), `firebase-config.js`, `core/firebase-init.js`, `index.html`, `partials/*`
- করবে: `config.js` (`window.MX`), নতুন API ক্লায়েন্ট (Bearer টোকেন, ৪০১ এ একবার রিট্রাই, ETag + IndexedDB ক্যাশ, ওয়ার্কার/মেইন রাউটিং ও ফেইলওভার, লগআউটে ক্যাশ মুছা); RTDB SDK ট্যাগ সরানো

**P18: ফ্রন্টএন্ড কল-সাইট (১): core + features** (ভার ৩) · §১১
- ফাইল: `bootstrap.js`, `core/*` (auth, user-record, user-data, presence, state…), `features/*` (user-lists, auth-forms, broken-link-reporter), `pages/profile.html`, `my-list.html`
- করবে: সব `db.ref().once/on/set/update/transaction` কল নতুন ফাংশনে বদল; পাসওয়ার্ড রিসেট → `/auth/password-reset`

**P19: ফ্রন্টএন্ড কল-সাইট (২): pages + GitHub Pages** (ভার ৩.৫) · §১১
- ফাইল: `watch-player.js`(৩৩KB), `search.js`(১২KB), `home.js`, `home-carousel.js`, `categories.js`, `category-movies.js`, `notifications.js`
- করবে: কল-সাইট বদল; আচরণ অক্ষত (অসীম স্ক্রল, ক্যাশ-ফার্স্ট, ৬০সে পোল, প্রগ্রেস ৩০সে); আপেক্ষিক পাথ, `.nojekyll`, `.github/workflows/pages.yml`

### ধাপ E: ডেলিভারি (P20)

**P20: হার্ডেনিং + ডেলিভারি** (ভার ৪) · §১২ §১৩ §১৪.৯ §১৫
- করবে: Dockerfile (multi-stage, non-root), `.env.example`, বাংলা README (লোকাল, env, Botkeep/Docker/VPS/systemd, ওয়ার্কার, টেলিগ্রাম সেটআপ, রিস্টোর, GitHub Pages, সীমাবদ্ধতা), `go test -race ./...`, `go vet`, `staticcheck`, Playwright স্মোক টেস্ট, **§৭ সিকিউরিটি চেকলিস্ট রিভিউ**, `legacy/`, `database.rules.json`, `FIREBASE-RULES.md` মোছার প্রস্তাব (তোমার সম্মতি নিয়ে)
- চূড়ান্ত বাংলা সারাংশ

---

## ৫. ভার-সারণি (এক নজরে)

| ধাপ | পার্ট | মোট ভার |
|---|---|---|
| A কোর | P1–P6 | ১৭.৫ |
| B অ্যাডমিন | P7–P12 | ১৯.৫ |
| C অপারেশন | P13–P16 | ১৩.৫ |
| D ফ্রন্টএন্ড | P17–P19 | ৯.৫ |
| E ডেলিভারি | P20 | ৪ |
| **মোট** | **২০** | **৬৪** |

## ৬. নির্ভরতার ক্রম (ক্রম না ভাঙতে)

```
P1 → P2 → P3 → P4 → P5 → P6
              └──────────────→ P7 → P8..P12 (P7 এর পর যেকোনো ক্রমে)
P2 → P13 → P14          (ব্যাকআপ শুধু DB স্কিমা থাকলেই হয়)
P2 → P15 → P16          (changes লগ P2 তেই বানানো)
P5,P6 শেষ → P17 → P18 → P19
সবশেষে → P20
```
> সহজ নিয়ম: **P1 থেকে P20 সংখ্যার ক্রমেই চালাও**। এটাই নিরাপদ।
