import { describe, it, expect } from 'vitest';
// Mirror of internal/eolagg/testdata/fixtures.json — copied into the UI
// tree so the Dockerfile's ui-build stage (which only mounts ui/) can
// resolve the import. The Go test reads the canonical file; both must
// stay byte-identical (verified by the parity check below + a CI guard
// in internal/eolagg/testdata/README.md).
import fixtures from '../__testdata__/eolagg-fixtures.json';

describe('EOL aggregator parity', () => {
  it('emits the same (entity_type, product) pairs as the Go aggregator', () => {
    const PREFIX = 'longue-vue.io/eol.';
    const got = new Set<string>();
    for (const c of fixtures.clusters) {
      for (const key of Object.keys(c.annotations ?? {})) {
        if (key.startsWith(PREFIX)) got.add(`cluster:${key.slice(PREFIX.length)}`);
      }
    }
    for (const n of fixtures.nodes) {
      for (const key of Object.keys(n.annotations ?? {})) {
        if (key.startsWith(PREFIX)) got.add(`node:${key.slice(PREFIX.length)}`);
      }
    }
    for (const v of fixtures.vms) {
      for (const key of Object.keys(v.annotations ?? {})) {
        if (key.startsWith(PREFIX)) got.add(`vm:${key.slice(PREFIX.length)}`);
      }
    }
    const want = new Set<string>([
      'cluster:kubernetes',
      'node:ubuntu',
      'node:containerd',
      'vm:vault',
      'vm:cyberwatch',
    ]);
    expect(got).toEqual(want);
  });
});
