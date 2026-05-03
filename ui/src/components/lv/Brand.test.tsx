import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Brand } from './Brand';

describe('Brand', () => {
  it('renders the wordmark with italic dot in accent', () => {
    const { container } = render(<Brand />);
    const wordmark = container.querySelector('.lv-brand-name');
    expect(wordmark?.textContent).toBe('Longue·vue');
    expect(wordmark?.querySelector('em')?.textContent).toBe('·');
  });

  it('renders the logomark', () => {
    const { container } = render(<Brand />);
    expect(container.querySelector('svg')).toBeTruthy();
  });
});
