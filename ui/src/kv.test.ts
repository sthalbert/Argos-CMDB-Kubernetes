import { describe, expect, it } from 'vitest';
import { formatKV, parseKV } from './kv';

describe('formatKV', () => {
  it('returns empty string for null / undefined', () => {
    expect(formatKV(null)).toBe('');
    expect(formatKV(undefined)).toBe('');
  });

  it('returns key=value lines joined by newline', () => {
    expect(formatKV({ a: '1', b: '2' })).toBe('a=1\nb=2');
  });

  it('handles empty object', () => {
    expect(formatKV({})).toBe('');
  });
});

describe('parseKV', () => {
  it('parses key=value lines', () => {
    expect(parseKV('a=1\nb=2', 'labels')).toEqual({ a: '1', b: '2' });
  });

  it('trims whitespace and ignores blank lines', () => {
    expect(parseKV('  a = 1 \n\n b=2 \n', 'labels')).toEqual({ a: '1', b: '2' });
  });

  it('returns empty object for empty input', () => {
    expect(parseKV('', 'labels')).toEqual({});
    expect(parseKV('   \n  \n', 'labels')).toEqual({});
  });

  it('preserves "=" inside the value', () => {
    expect(parseKV('url=https://x?a=1', 'labels')).toEqual({ url: 'https://x?a=1' });
  });

  it('throws on missing "="', () => {
    expect(() => parseKV('badline', 'labels')).toThrow(/labels: expected key=value/);
  });

  it('throws on empty key', () => {
    // eq=0 means no chars before '=', triggers the eq <= 0 branch
    expect(() => parseKV('=value', 'labels')).toThrow(/labels: expected key=value/);
  });
});
