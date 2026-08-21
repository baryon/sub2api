# Sub2API Docker Image

This fork does **not** publish `weishaw/sub2api`. Build the application image from the repository `Dockerfile`.

Postgres and Redis still use public images (`postgres:18-alpine`, `redis:8-alpine`).

## This fork's production (akiba / sub2api.chainbow.io)

Do **not** use `docker-compose.local.yml` on that host. Production is `docker-compose.yml` plus untracked `docker-compose.override.yml`.

Follow [`.cursor/skills/sub2api-production-release/SKILL.md`](../.cursor/skills/sub2api-production-release/SKILL.md). Short form:

```bash
ssh -p 220 -o BatchMode=yes deploy@122.208.117.197 'bash -s' << 'REMOTE'
set -euo pipefail
unset COMPOSE_FILE
cd /home/deploy/sub2api
git pull --ff-only origin main
cd deploy
docker compose up -d --build
REMOTE
```

## Generic self-host (not akiba)

`docker-compose.local.yml` is for a fresh checkout with `./data`, `./postgres_data`, `./redis_data`. It shares container names with production but **not** data dirs or networks. Never point it at akiba.

```bash
cd deploy
docker compose -f docker-compose.local.yml up -d --build
```

The Hub-oriented snippets below are retained only as a reference for the upstream project.

## Quick Start (upstream Hub image, not this fork)

```bash
docker run -d \
  --name sub2api \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/sub2api" \
  -e REDIS_URL="redis://host:6379" \
  weishaw/sub2api:latest
```

## Docker Compose

```yaml
version: '3.8'

services:
  sub2api:
    image: weishaw/sub2api:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://postgres:postgres@db:5432/sub2api?sslmode=disable
      - REDIS_URL=redis://redis:6379
    depends_on:
      - db
      - redis

  db:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=sub2api
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Yes | - |
| `REDIS_URL` | Redis connection string | Yes | - |
| `PORT` | Server port | No | `8080` |
| `GIN_MODE` | Gin framework mode (`debug`/`release`) | No | `release` |

## Supported Architectures

- `linux/amd64`
- `linux/arm64`

## Tags

- `latest` - Latest stable release
- `x.y.z` - Specific version
- `x.y` - Latest patch of minor version
- `x` - Latest minor of major version

## Links

- [GitHub Repository](https://github.com/weishaw/sub2api)
- [Documentation](https://github.com/weishaw/sub2api#readme)
