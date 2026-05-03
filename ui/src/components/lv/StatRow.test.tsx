import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StatRow, Stat } from './StatRow';

describe('StatRow', () => {
  it('renders children inside .lv-stat-row', () => {
    const { container } = render(
      <StatRow>
        <Stat label="Pods" value={42} />
        <Stat label="Up" value="100%" tone="ok" meta="2/2" />
      </StatRow>,
    );
    expect(container.querySelector('.lv-stat-row')).toBeTruthy();
    expect(container.querySelectorAll('.lv-stat').length).toBe(2);
  });

  it('renders label/value/meta and applies tone class', () => {
    const { container } = render(<Stat label="Drift" value="3" tone="bad" meta="last 1h" />);
    expect(container.querySelector('.lv-stat-label')?.textContent).toBe('Drift');
    const v = container.querySelector('.lv-stat-value')!;
    expect(v.textContent).toBe('3');
    expect(v.classList.contains('bad')).toBe(true);
    expect(container.querySelector('.lv-stat-meta')?.textContent).toBe('last 1h');
  });

  it('omits meta when not given', () => {
    const { container } = render(<Stat label="x" value="1" />);
    expect(container.querySelector('.lv-stat-meta')).toBeNull();
  });
});
