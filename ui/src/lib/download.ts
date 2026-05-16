export interface DownloadResult {
  truncated: boolean;
  filename: string | null;
}

export function parseContentDispositionFilename(header: string | null): string | null {
  if (!header) return null;
  // attachment; filename="x.csv"  or  attachment; filename=x.csv
  const match = header.match(/filename\*?=("?)([^";]+)\1/);
  return match ? match[2] : null;
}

/**
 * Download an extract URL as a Blob, surface the X-Longue-Vue-Truncated
 * header, and trigger a synthetic <a download> so the browser saves the
 * file. The session cookie / PAT travels via `credentials: 'include'`.
 */
export async function downloadExtract(
  url: string,
  fallbackFilename: string,
): Promise<DownloadResult> {
  const resp = await fetch(url, { credentials: 'include' });
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`extract failed (${resp.status}): ${body}`);
  }
  const truncated = resp.headers.get('X-Longue-Vue-Truncated') === 'true';
  const filename = parseContentDispositionFilename(resp.headers.get('Content-Disposition'));
  const blob = await resp.blob();

  const objectURL = URL.createObjectURL(blob);
  try {
    const a = document.createElement('a');
    a.href = objectURL;
    a.download = filename ?? fallbackFilename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  } finally {
    URL.revokeObjectURL(objectURL);
  }
  return { truncated, filename };
}
