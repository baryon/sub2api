---
name: sub2api-production-release
description: >-
  Publish a new sub2api.chainbow.io version: commit reviewed changes, push
  origin/main, then rebuild production on akiba. Use when the user says 上线吧,
  上线, 发布, 发布新版, deploy, production deploy, or asks how to ship this fork.
---

# Production release (sub2api.chainbow.io)

This is the baryon/chainbow fork, not upstream Weishaw. Product fixes are server-side only; do not tell users to edit `~/.codex`.

「上线吧 / 发布」means: commit remaining reviewed work if needed, **push**, then production deploy. That phrase is authorization. Do not force-push. Do not amend a commit that is already on the remote.

## Production facts

| Item | Value |
| --- | --- |
| Public URL | `https://sub2api.chainbow.io` |
| SSH | `ssh -p 220 deploy@122.208.117.197` |
| Host | `akiba` / `akiba.chainbow.io` (same key as the IP) |
| User | `deploy` |
| Repo | `/home/deploy/sub2api` |
| Origin | `git@github.com:baryon/sub2api.git` |
| Branch | `main` |
| Compose cwd | `/home/deploy/sub2api/deploy` |
| Compose files | `docker-compose.yml` + untracked `docker-compose.override.yml` (auto-merged; `COMPOSE_FILE` unset) |
| App image | `sub2api:local` (`pull_policy: never`, built from repo-root `Dockerfile`) |
| Data | `deploy/data/{postgres,redis,sub2api}` via override, not named volumes |

There is no SSH Host alias `chainbow-deploy`. Do not use `chainbow-ropsten` / `chainbow-mainnet`. Public DNS goes through Cloudflare + nginx-proxy; do not publish host port 8080.

## Local: commit and push

1. Review. Run the affected Go tests. Do not ship secrets (`.env`, credentials).
2. Commit with repo style: `fix:` / `feat:` focusing on **why**. Example:

```bash
git add <files>
git commit -m "$(cat <<'EOF'
fix: keep official OpenAI Responses cache fields on compatible-safe accounts

EOF
)"
```

3. `git push origin HEAD` (usually `main`). Never `--force` to `main`. Never `--no-verify` unless the user asked.

Frontend is embedded in the Go image. A code change is not live until the server rebuilds.

## Server: pull and rebuild

Run from this machine. `git pull` must be `--ff-only`. Compose **must** run in `deploy/` so the untracked override is applied.

```bash
ssh -p 220 -o BatchMode=yes deploy@122.208.117.197 'bash -s' << 'REMOTE'
set -euo pipefail
cd /home/deploy/sub2api
git pull --ff-only origin main
cd deploy
docker compose up -d --build
docker compose ps
docker inspect --format '{{.State.Status}} {{.State.Health.Status}}' sub2api
git log -1 --oneline
REMOTE
```

Wait until inspect prints `running healthy`. Build can take several minutes (frontend + Go). If health is still starting, poll; do not restart blindly.

Optional checks after healthy:

```bash
ssh -p 220 -o BatchMode=yes deploy@122.208.117.197 'docker exec sub2api wget -q -T 5 -O - http://localhost:8080/health'
curl -fsS https://sub2api.chainbow.io/health
```

Server HEAD must match the commit just pushed.

## Do not

- `./deploy/docker-deploy.sh` (rewrites `deploy/.env`)
- `docker compose down -v` or delete `deploy/data/`
- `docker compose -f docker-compose.local.yml` / `dev.yml` / `standalone.yml` (wrong data dirs and container names)
- compose from the **repo root** (override will not load)
- `docker compose pull` for the app image (it is local-only)
- force-push, amend after push, or change git config

`up -d --build` rebuilds the **app** container. Postgres/Redis stay on the existing data directory. Schema migrations are forward-only; `AUTO_SETUP` will not wipe an existing admin.

## After deploy

Report: local commit SHAs, remote HEAD on `akiba`, container health, and `/health`. Stop there unless the user asked for log digging.
