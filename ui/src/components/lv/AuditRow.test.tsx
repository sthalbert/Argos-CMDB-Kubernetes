import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { AuditHeader, AuditRow } from './AuditRow';

describe('AuditRow', () => {
  it('renders all four columns with ARIA roles', () => {
    const { container } = render(
      <AuditRow time="2026-05-01 09:14" actor="alice" message="updated cluster prod" result="ok" />,
    );
    const row = container.querySelector('[role="row"]')!;
    expect(row).toBeTruthy();
    expect(row.querySelector('.lv-audit-time')?.getAttribute('role')).toBe('cell');
    expect(row.querySelector('.lv-audit-time')?.textContent).toBe('2026-05-01 09:14');
    expect(row.querySelector('.lv-audit-actor')?.textContent).toBe('alice');
    expect(row.querySelector('.lv-audit-msg')?.textContent).toBe('updated cluster prod');
    expect(row.querySelector('.lv-audit-result')?.textContent).toBe('ok');
  });

  it('renders the meta (source IP) slot when provided', () => {
    const { container } = render(
      <AuditRow
        time="2026-05-01 09:14"
        actor="alice"
        message="cluster.update"
        result="200"
        meta={<code>198.51.100.1</code>}
      />,
    );
    expect(container.querySelector('.lv-audit-meta')?.textContent).toBe('198.51.100.1');
    expect(container.querySelector('.lv-audit-meta')?.getAttribute('role')).toBe('cell');
  });

  it('omits the meta slot when not provided', () => {
    const { container } = render(
      <AuditRow time="2026-05-01 09:14" actor="alice" message="cluster.update" result="200" />,
    );
    expect(container.querySelector('.lv-audit-meta')).toBeNull();
  });
});

describe('AuditHeader', () => {
  it('renders a header row with five columnheader cells', () => {
    const { container } = render(<AuditHeader />);
    const row = container.querySelector('[role="row"]')!;
    expect(row).toBeTruthy();
    const headers = row.querySelectorAll('[role="columnheader"]');
    expect(headers).toHaveLength(5);
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toEqual(['Time', 'Actor', 'Message', 'HTTP', 'Source IP']);
  });
});
