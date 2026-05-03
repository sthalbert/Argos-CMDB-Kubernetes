import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { AuditRow } from './AuditRow';

describe('AuditRow', () => {
  it('renders all four columns', () => {
    const { container } = render(
      <AuditRow time="2026-05-01 09:14" actor="alice" message="updated cluster prod" result="ok" />,
    );
    const row = container.querySelector('.lv-audit-row')!;
    expect(row.querySelector('.lv-audit-time')?.textContent).toBe('2026-05-01 09:14');
    expect(row.querySelector('.lv-audit-actor')?.textContent).toBe('alice');
    expect(row.querySelector('.lv-audit-msg')?.textContent).toBe('updated cluster prod');
    expect(row.querySelector('.lv-audit-result')?.textContent).toBe('ok');
  });
});
