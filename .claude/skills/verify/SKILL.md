---
name: verify
description: Build and run 轻舟 (qingzhou) locally to observe a change end-to-end — boot the Go binary against a throwaway SQLite DB, drive the HTTP API and the Vue admin/user UI.
---

# Verifying qingzhou

Single Go binary serving a JSON API + an embedded Vue frontend, backed by SQLite.
The surface is **HTTP** (and the browser UI it serves).

## Build

```bash
go build -o /tmp/qz-verify .        # backend
cd frontend && npx vite build       # frontend → frontend/dist, embedded by the Go build
```

Rebuild the Go binary **after** `vite build` — `frontend/dist` is embedded at
compile time, so a stale binary serves stale UI.

- `npm run build` runs `vue-tsc -b` first and **fails with ~40 pre-existing TS
  errors** (`bordered="false"`, missing `@types/qrcode`). That's the state of
  `main`, not your change. The Dockerfile uses `npx vite build`, which skips
  typecheck — use that.

## Run

Everything is env-configurable, so never touch the real `qingzhou.db`:

```bash
QZ_LISTEN=127.0.0.1:8099 \
QZ_DB=/tmp/verify.db \
QZ_ADMIN_USER=admin QZ_ADMIN_PASS=admin12345 \
/tmp/qz-verify
```

The admin is seeded on first boot from `QZ_ADMIN_USER`/`QZ_ADMIN_PASS`.

### The sing-box dependency (the main gotcha)

**Creating any user** — admin API or self-registration — calls `provisionClient`,
which shells out to `sing-box check` and `systemctl restart`. Off Linux (or with
no sing-box installed) user creation fails with
`502 开通节点失败：... "sing-box": executable file not found`, and the user is
rolled back.

Stub both binaries — they're external deps, not the code under test:

```bash
mkdir -p /tmp/stub
printf '#!/bin/sh\nexit 0\n' > /tmp/stub/sing-box   && chmod +x /tmp/stub/sing-box
printf '#!/bin/sh\nexit 0\n' > /tmp/stub/systemctl  && chmod +x /tmp/stub/systemctl
export PATH=/tmp/stub:$PATH
export QZ_SINGBOX_BIN=/tmp/stub/sing-box
export QZ_SINGBOX_CONFIG=/tmp/sb-config.json   # else it writes /etc/sing-box/config.json
```

On Windows use `.cmd` stubs (`@echo off` / `exit /b 0`); `systemctl` must be
findable on `PATH`, and `sing-box` via `QZ_SINGBOX_BIN`.

This only fakes the proxy daemon. Panel logic (billing, gating, subscriptions)
still runs for real.

## Drive it

Auth is a bearer JWT from `POST /api/auth/login` → `.data.token`; send it as
`Authorization: Bearer <token>`. Responses are `{code, data, msg}` — `code` is
the HTTP status, and `msg` is the user-facing Chinese error worth quoting as
evidence.

```bash
TOK=$(curl -s localhost:8099/api/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin12345"}' | jq -r .data.token)
curl -s localhost:8099/api/admin/packages -H "Authorization: Bearer $TOK" | jq
```

Registration is **closed by default** and email verification is **on**. To
exercise the signup flow:

```bash
curl -s -X PUT localhost:8099/api/admin/settings -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d '{"register_mode":"code","email_verify_required":"false"}'
```
(`register_mode`: `open` | `code` | `closed`.)

### Browser UI

Hash router: `http://127.0.0.1:8099/#/admin/packages`. The token lives in
`localStorage` under **`qz_token`** — set it and reload to switch users without
retyping a login:

```js
fetch('/api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},
  body:JSON.stringify({username:'someone',password:'pass12345'})})
  .then(r=>r.json()).then(j=>{localStorage.setItem('qz_token',j.data.token);
    location.hash='#/shop'; location.reload();});
```

`get_page_text` is the reliable read. **Screenshots time out** on `/` — the
monitor page runs a 30s auto-refresh + echarts animation that keeps the renderer
busy. Navigate to an inner admin page first, or just read page text.

`frontend/` is the only frontend (the legacy source-less `web/` UI and its
`QZ_USE_NEW_FRONTEND` switch were removed). `frontend/dist` is embedded at
`go build` time and is not committed, so **build the frontend before the binary**
or you get a blank page.

## Worth knowing

- Two independent "group" axes, easy to confuse: **node groups**
  (`node_groups`/`plan_groups`) = which nodes a bought plan grants; **user groups**
  (`user_groups`/`package_user_groups`) = who may buy a package.
- Purchase gating is enforced in two places and both need driving: the shop
  listing (`ListPackagesForUser`, decides *visible*) and inside the `Purchase`
  transaction (decides *buyable*). Probe the gate by POSTing a hidden
  `package_id` straight to `/api/user/purchase` — the listing alone is not a gate.
- `POST /api/admin/users/{id}/assign-plan` deliberately bypasses purchase-time
  checks (admin comp path).
