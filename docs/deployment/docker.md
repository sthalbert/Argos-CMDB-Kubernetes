# Run longue-vue with Docker

This guide covers running longue-vue locally with Docker for development, testing, and demos.

## Start PostgreSQL

```bash
docker run -d --rm --name longue-vue-pg \
  -e POSTGRES_PASSWORD=longue-vue -e POSTGRES_DB=longue-vue \
  -p 5432:5432 postgres:16-alpine
```

Verify it is ready:

```bash
docker exec longue-vue-pg pg_isready
```

## Build and run longue-vue

### With the UI (full build)

```bash
make ui-install    # first time only
make ui-build      # produces ui/dist/
make build         # produces bin/longue-vue
```

### Without the UI (backend-only)

```bash
make build-noui    # no Node/npm required; /ui/ returns 404
```

### Start longue-vue

```bash
LONGUE_VUE_DATABASE_URL="postgres://postgres:longue-vue@localhost:5432/longue-vue?sslmode=disable" \
  LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="local-dev-passphrase" \
  ./bin/longue-vue
```

longue-vue listens on `http://localhost:8080` by default.

### Using the Docker image

If you prefer to run longue-vue itself in a container:

```bash
make docker-build    # tags longue-vue:dev

docker run --rm \
  --network host \
  -e LONGUE_VUE_DATABASE_URL="postgres://postgres:longue-vue@localhost:5432/longue-vue?sslmode=disable" \
  -e LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="local-dev-passphrase" \
  longue-vue:dev
```

On macOS, replace `--network host` with `-p 8080:8080` and use `host.docker.internal` instead of `localhost` in the DSN:

```bash
docker run --rm \
  -p 8080:8080 \
  -e LONGUE_VUE_DATABASE_URL="postgres://postgres:longue-vue@host.docker.internal:5432/longue-vue?sslmode=disable" \
  -e LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="local-dev-passphrase" \
  longue-vue:dev
```

## Seed demo data

The demo seed script populates a realistic multi-cluster inventory without needing a real Kubernetes cluster:

```bash
# 1. Log in and create a token.
curl -sS -c /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your password after rotation>"}'

curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"seed","scopes":["read","write","delete"]}'

# 2. Run the seed script with the token.
LONGUE_VUE_URL=http://localhost:8080 LONGUE_VUE_TOKEN=longue_vue_pat_... ./scripts/seed-demo.sh
```

The script creates two clusters (prod, staging) with namespaces, workloads, pods, services, and a MetalLB-style ingress. Re-runnable after a `TRUNCATE clusters CASCADE` in PostgreSQL.

## UI access

Open `http://localhost:8080/` in a browser. It redirects to `/ui/`.

Sign in with:
- **Username:** `admin`
- **Password:** `local-dev-passphrase` (or whatever you set above; rotated on first login)

## Development workflow

For iterative development, run longue-vue and the Vite dev server in parallel for hot reload:

### Terminal 1 -- longue-vue

```bash
LONGUE_VUE_DATABASE_URL="postgres://postgres:longue-vue@localhost:5432/longue-vue?sslmode=disable" \
  LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="local-dev-passphrase" \
  ./bin/longue-vue
```

### Terminal 2 -- Vite dev server

```bash
make ui-dev
```

This starts the Vite dev server on `http://localhost:5173` and proxies `/v1`, `/healthz`, and `/metrics` to longue-vue on `:8080`. Edit React/TypeScript files and see changes instantly.

Open `http://localhost:5173/ui/` and sign in with the admin credentials.

### Backend-only development

If you are only working on Go code:

```bash
make build-noui && ./bin/longue-vue   # quick rebuild, no UI toolchain
make test                          # run all tests with -race
make test-one TEST=TestMyFunction  # run a single test
```

### Integration tests

Integration tests that hit PostgreSQL are gated on `PGX_TEST_DATABASE`:

```bash
PGX_TEST_DATABASE="postgres://postgres:longue-vue@localhost:5432/longue-vue?sslmode=disable" \
  make test
```

Without `PGX_TEST_DATABASE`, those tests skip automatically.

## Make targets reference

| Target | What it does |
|--------|--------------|
| `make build` | Compile longue-vue into `bin/` (embeds `ui/dist`). |
| `make build-noui` | Compile without the UI (no Node required). |
| `make build-collector` | Compile the push-mode collector into `bin/`. |
| `make test` | `go test -race -cover ./...` |
| `make check` | fmt + vet + lint + test (CI-equivalent). |
| `make docker-build` | Build the longue-vue container image as `longue-vue:dev`. |
| `make docker-build-collector` | Build the collector image as `longue-vue-collector:dev`. |
| `make ui-install` | `npm ci` in `ui/`. |
| `make ui-build` | Produce `ui/dist/`. |
| `make ui-dev` | Vite dev server on `:5173`. |
| `make ui-check` | TypeScript typecheck. |

## Cleanup

```bash
docker stop longue-vue-pg
```

This removes the PostgreSQL container and all its data (the `--rm` flag was set at creation).
