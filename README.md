# Names

Anonymous name-voting site. Submit names, vote 👍/👎, browse top/controversial/new, search. Admin can audit and remove names. Deployed at https://names.thomasjbarlow.com.

## Stack

- Go 1.25 + Gin + GORM + PostgreSQL 16
- HTMX + Go `html/template` for the UI (no Node build)
- Docker Swarm deployment, Traefik routing, Cloudflare TLS
- GitHub Actions CI/CD on a self-hosted runner, pushing to `registry.thomasjbarlow.com`

## Local development

```sh
cp secrets/names.env.example secrets/names.env  # then edit
task up:local:backend
```

Open http://localhost:8104.

## Production

PR opened → image built and pushed. PR merged → stack redeployed on `mediapc` swarm node.

See [.github/workflows/Build.yml](.github/workflows/Build.yml) and [.github/workflows/Deploy.yml](.github/workflows/Deploy.yml).

## Required GitHub repository secrets

- `DOCKER_REGISTRY_USERNAME`, `DOCKER_REGISTRY_PASSWORD`
- `DATABASE_USERNAME`, `DATABASE_PASSWORD`, `DATABASE_NAME`
- `ADMIN_USERNAME`
- `ADMIN_PASSWORD_BCRYPT` — bcrypt hash. Generate with: `htpasswd -nbBC 12 "" 'yourpass' | tr -d ':\n' | sed 's/^\$2y/\$2a/'`
- `ADMIN_JWT_SECRET` — 64 random hex chars: `openssl rand -hex 32`
- `VOTER_SALT` — 32 random hex chars: `openssl rand -hex 16`

## One-time swarm setup

```sh
docker volume create names-data-drive
# The traefik-public overlay network already exists on mediapc.
```
