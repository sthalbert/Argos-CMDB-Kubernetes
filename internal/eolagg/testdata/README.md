## Shared EOL aggregator fixtures

`fixtures.json` in this directory is the canonical EOL-aggregator corpus.
The Go test `internal/eolagg/aggregate_test.go` loads it directly.

A byte-identical copy lives at `ui/src/__testdata__/eolagg-fixtures.json`
because the Dockerfile's `ui-build` stage only mounts `ui/` and cannot
reach a parent path during `tsc --noEmit`. The UI parity test
`ui/src/pages/EolDashboard.fixtures.test.tsx` loads the copy.

When editing the corpus, update both files. The CI parity test fails
loudly if the two aggregators emit different `(entity_type, product)`
pairs, but it does not currently catch byte-level drift between the two
JSON files — keep them in sync by hand.
