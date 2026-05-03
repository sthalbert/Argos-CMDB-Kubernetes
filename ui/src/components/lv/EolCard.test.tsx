import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { EolCard } from './EolCard';

describe('EolCard', () => {
  it('renders count, label, meta with the right modifier class', () => {
    const { container } = render(
      <EolCard status="bad" count={3} label="Past EOL" meta="critical" />,
    );
    const card = container.querySelector('.eol-summary-card')!;
    expect(card.classList.contains('eol-bad')).toBe(true);
    expect(card.classList.contains('bad')).toBe(true);
    expect(card.querySelector('.eol-count')?.textContent).toBe('3');
    expect(card.querySelector('.eol-label')?.textContent).toBe('Past EOL');
    expect(card.querySelector('.eol-meta')?.textContent).toBe('critical');
  });

  it('marks .active when active=true', () => {
    const { container } = render(<EolCard status="ok" count={0} label="Safe" meta="" active />);
    expect(container.querySelector('.eol-summary-card')?.classList.contains('active')).toBe(true);
  });

  it('fires onClick when clicked', () => {
    const onClick = vi.fn();
    const { getByRole } = render(<EolCard status="warn" count={1} label="x" meta="y" onClick={onClick} />);
    fireEvent.click(getByRole('button'));
    expect(onClick).toHaveBeenCalled();
  });

  it('renders a non-button div when no onClick is given', () => {
    const { container } = render(<EolCard status="ok" count={0} label="x" meta="y" />);
    expect(container.querySelector('button')).toBeNull();
    expect(container.querySelector('div.eol-summary-card')).toBeTruthy();
  });
});
