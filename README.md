# Names

Anonymous name-voting site. Submit names, vote 👍/👎, browse top/controversial/new, search. Admin can audit and remove submissions.

## Stack

- Go 1.25 + Gin + GORM + PostgreSQL 16
- HTMX + Go `html/template` for the UI (no Node build step)
- Docker Swarm deployment behind Traefik with Cloudflare TLS
- GitHub Actions CI/CD on a self-hosted runner, pushing to a private container registry

## Local development

```sh
cp secrets/names.env.example secrets/names.env   # then edit the values
task up:local:backend
```

Open http://localhost:8104.

## Production

A maintainer opens a PR → the image is built and pushed. The maintainer merges to `main` → the stack is redeployed.

See [.github/workflows/Build.yml](.github/workflows/Build.yml) and [.github/workflows/Deploy.yml](.github/workflows/Deploy.yml).

## Contributing

External contributions are welcome via pull request. CI **does not run automatically** on PRs from forks — a maintainer must review the diff and trigger a build manually. This protects the self-hosted runner and production secrets from untrusted code.
