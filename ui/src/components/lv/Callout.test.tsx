import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Callout } from './Callout';

describe('Callout', () => {
  it('renders a div.lv-callout with title and body', () => {
    const { container } = render(<Callout title="Heads up">body text</Callout>);
    const root = container.querySelector('.lv-callout')!;
    expect(root.querySelector('strong')?.textContent).toBe('Heads up');
    expect(root.textContent).toContain('body text');
  });

  it.each(['warn', 'bad', 'ok'] as const)('adds the %s tone modifier', (status) => {
    const { container } = render(<Callout title="x" status={status}>y</Callout>);
    expect(container.querySelector('.lv-callout')?.classList.contains(status)).toBe(true);
  });

  it('renders without a body', () => {
    const { container } = render(<Callout title="title-only" />);
    expect(container.querySelector('.lv-callout strong')?.textContent).toBe('title-only');
  });
});
