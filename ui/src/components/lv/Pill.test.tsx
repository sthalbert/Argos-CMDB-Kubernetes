import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Pill } from './Pill';

describe('Pill', () => {
  it('renders <span class="pill"> with no modifier when no status given', () => {
    const { container } = render(<Pill>hello</Pill>);
    const el = container.querySelector('span.pill');
    expect(el?.className.trim()).toBe('pill');
    expect(el?.textContent).toBe('hello');
  });

  it.each(['ok', 'warn', 'bad', 'accent'] as const)('adds the %s status modifier', (status) => {
    const { container } = render(<Pill status={status}>x</Pill>);
    const el = container.querySelector('span.pill');
    expect(el?.classList.contains(status)).toBe(true);
  });

  it('passes className through', () => {
    const { container } = render(<Pill className="extra">x</Pill>);
    expect(container.querySelector('span.pill')?.classList.contains('extra')).toBe(true);
  });
});
