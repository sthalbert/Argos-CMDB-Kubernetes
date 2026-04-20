// Shared serializers for Record<string, string> columns edited in the
// UI as one `key=value` per line. Used for `labels` and `annotations`
// on every kind that exposes them. Kept as plain strings (not JSON) so
// the user never sees a JSON-parse error for a trailing comma.

export function formatKV(m?: Record<string, string> | null): string {
  if (!m) return '';
  return Object.entries(m)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n');
}

export function parseKV(text: string, fieldName: string): Record<string, string> {
  const out: Record<string, string> = {};
  const lines = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
  if (lines.length === 0) return {};
  for (const line of lines) {
    const eq = line.indexOf('=');
    if (eq <= 0) {
      throw new Error(`${fieldName}: expected key=value, got ${line.slice(0, 40)}`);
    }
    const k = line.slice(0, eq).trim();
    const v = line.slice(eq + 1).trim();
    if (!k) throw new Error(`${fieldName}: empty key in line ${line.slice(0, 40)}`);
    out[k] = v;
  }
  return out;
}
