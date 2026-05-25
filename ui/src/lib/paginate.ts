// Walk the server's cursor pagination until exhausted and return every
// item. List pages render a single table without per-page navigation, so
// the UI must aggregate all pages itself — a single first-page call
// silently hides rows beyond the server's default page size.

import type { PagedResponse } from '../api';

export async function fetchAllPages<T>(
  fetchPage: (cursor?: string) => Promise<PagedResponse<T>>,
): Promise<{ items: T[] }> {
  const items: T[] = [];
  let cursor: string | undefined = undefined;
  for (let i = 0; i < 1000; i++) {
    const page = await fetchPage(cursor);
    items.push(...page.items);
    if (!page.next_cursor) break;
    cursor = page.next_cursor;
  }
  return { items };
}
