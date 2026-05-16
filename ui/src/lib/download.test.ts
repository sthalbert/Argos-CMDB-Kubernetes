import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { downloadExtract, parseContentDispositionFilename } from './download';

describe('parseContentDispositionFilename', () => {
  it('extracts filename from a simple header', () => {
    expect(parseContentDispositionFilename('attachment; filename="my-file.csv"')).toBe('my-file.csv');
  });
  it('handles missing quotes', () => {
    expect(parseContentDispositionFilename('attachment; filename=my-file.csv')).toBe('my-file.csv');
  });
  it('returns null when no filename present', () => {
    expect(parseContentDispositionFilename('attachment')).toBeNull();
    expect(parseContentDispositionFilename(null)).toBeNull();
  });
});

describe('downloadExtract', () => {
  beforeEach(() => {
    Object.defineProperty(URL, 'createObjectURL', { value: vi.fn(() => 'blob:fake'), configurable: true });
    Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('surfaces the truncation flag from the response header', async () => {
    const headers = new Headers({
      'Content-Type': 'text/csv',
      'Content-Disposition': 'attachment; filename="out.csv"',
      'X-Longue-Vue-Truncated': 'true',
    });
    vi.spyOn(global, 'fetch').mockResolvedValue(new Response('data', { status: 200, headers }));
    const result = await downloadExtract('/v1/eol/extract?format=csv', 'fallback.csv');
    expect(result.truncated).toBe(true);
    expect(result.filename).toBe('out.csv');
  });

  it('treats absent truncation header as false', async () => {
    const headers = new Headers({ 'Content-Type': 'text/csv' });
    vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response('x', { status: 200, headers }),
    );
    const result = await downloadExtract('/url', 'fallback.csv');
    expect(result.truncated).toBe(false);
  });

  it('throws on non-2xx responses', async () => {
    vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response('{"detail":"bad"}', { status: 400, headers: { 'Content-Type': 'application/problem+json' } }),
    );
    await expect(downloadExtract('/url', 'fallback.csv')).rejects.toThrow();
  });
});
