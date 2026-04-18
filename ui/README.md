# Argos UI

Vite + React + TypeScript SPA. Served by `argosd` at `/ui/` in production via
`go:embed`; run standalone during development.

See [ADR-0006](../docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md) for
scope and rationale.

## Development loop

```bash
# 1. Once per checkout (or after dep bumps):
make ui-install

# 2. Terminal A — start argosd (API + embedded production bundle):
ARGOS_DATABASE_URL=postgres://... ARGOS_API_TOKEN=dev ./bin/argosd

# 3. Terminal B — Vite dev server with HMR, proxies /v1 /healthz /metrics -> :8080:
make ui-dev
# → open http://localhost:5173/ui/
```

## Production build

```bash
make ui-build      # produces ui/dist/
make build         # argosd binary embeds ui/dist via //go:embed
```

## Skipping the UI

Backend-only workflows (no Node toolchain) can use `make build-noui`. `/ui/`
then replies 404; `/v1/*` is unaffected.

## Layout

```
ui/
├── embed.go              # //go:embed all:dist (default build)
├── embed_noui.go         # stub under -tags noui
├── vite.config.ts        # base: '/ui/' + dev proxy to argosd
├── tsconfig.json
├── package.json
├── index.html
└── src/
    ├── main.tsx          # router + strict mode
    ├── App.tsx           # routes + auth gate + chrome
    ├── api.ts            # hand-written typed fetch wrapper
    ├── styles.css
    └── pages/
        ├── Login.tsx     # bearer token -> sessionStorage
        └── Clusters.tsx  # first list page
```
