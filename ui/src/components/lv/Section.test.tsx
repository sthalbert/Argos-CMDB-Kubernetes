import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Section } from './Section';

describe('Section', () => {
  it('renders an h3.section-title with title and rule', () => {
    const { container } = render(<Section>Pods</Section>);
    const h3 = container.querySelector('h3.section-title')!;
    expect(h3.textContent).toContain('Pods');
    expect(h3.querySelector('.section-rule')).toBeTruthy();
  });

  it('renders a count when given', () => {
    const { container } = render(<Section count={12}>Pods</Section>);
    expect(container.querySelector('h3.section-title .count')?.textContent).toBe('· 12');
  });

  it('omits count span when count is undefined', () => {
    const { container } = render(<Section>Pods</Section>);
    expect(container.querySelector('h3.section-title .count')).toBeNull();
  });
});
