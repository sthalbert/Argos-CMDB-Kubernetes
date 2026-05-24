import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { EffectiveDICTCard } from './EffectiveDICTCard';
import { renderWithRouter } from '../../test/render';
import type { EffectiveDICT } from '../../api';

describe('EffectiveDICTCard', () => {
  it('shows the application hint when unclassified but linked', () => {
    renderWithRouter(
      <EffectiveDICTCard
        dict={{ source: 'none' }}
        linkedAppId="app-1"
        linkedAppName="billing"
      />,
    );
    expect(screen.getByText(/Not classified/)).toBeInTheDocument();
    expect(
      screen.getByText(/Set the security classification on application/),
    ).toBeInTheDocument();
    const link = screen.getByRole('link', { name: 'billing' });
    expect(link.getAttribute('href')).toBe('/applications/app-1');
  });

  it('shows the link-first hint when unclassified and not linked', () => {
    renderWithRouter(
      <EffectiveDICTCard dict={{ source: 'none' }} linkedAppId={null} linkedAppName={null} />,
    );
    expect(
      screen.getByText(/Link this resource to an application, then classify it/),
    ).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('treats a null/undefined dict as unclassified', () => {
    renderWithRouter(<EffectiveDICTCard dict={null} />);
    expect(
      screen.getByText(/Link this resource to an application, then classify it/),
    ).toBeInTheDocument();
  });

  it('renders the four axes with heat classes when classified by application', () => {
    const dict: EffectiveDICT = {
      disponibilite: 3,
      integrite: 2,
      confidentialite: 4,
      tracabilite: 1,
      source: 'application',
    };
    const { container } = renderWithRouter(
      <EffectiveDICTCard dict={dict} linkedAppId="app-9" linkedAppName="billing" />,
    );

    // All four axis abbreviations rendered.
    expect(screen.getByText('D')).toBeInTheDocument();
    expect(screen.getByText('I')).toBeInTheDocument();
    expect(screen.getByText('C')).toBeInTheDocument();
    expect(screen.getByText('T')).toBeInTheDocument();

    // Values present.
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();

    // Heat classes applied per value.
    expect(container.querySelector('.dict-value.heat-3')).not.toBeNull();
    expect(container.querySelector('.dict-value.heat-2')).not.toBeNull();
    expect(container.querySelector('.dict-value.heat-4')).not.toBeNull();
    expect(container.querySelector('.dict-value.heat-1')).not.toBeNull();

    // Source line links back to the application.
    const link = screen.getByRole('link', { name: 'billing' });
    expect(link.getAttribute('href')).toBe('/applications/app-9');
    expect(screen.getByText(/Inherited from application/)).toBeInTheDocument();
  });

  it('renders a neutral cell (no heat class) for a null axis', () => {
    const dict: EffectiveDICT = {
      disponibilite: 3,
      integrite: null,
      confidentialite: null,
      tracabilite: null,
      source: 'application',
    };
    const { container } = renderWithRouter(<EffectiveDICTCard dict={dict} />);
    // The em-dash placeholder renders for null axes...
    expect(screen.getAllByText('—').length).toBe(3);
    // ...and those cells carry no heat-* class (guards against heat-x).
    const neutral = container.querySelectorAll('.dict-value');
    const unstyled = Array.from(neutral).filter(
      (el) => !/heat-\d/.test(el.className) && el.textContent === '—',
    );
    expect(unstyled.length).toBe(3);
  });

  it('renders notes when present', () => {
    const dict: EffectiveDICT = {
      disponibilite: 2,
      notes: 'PII processing pipeline',
      source: 'application',
    };
    renderWithRouter(<EffectiveDICTCard dict={dict} />);
    expect(screen.getByText('PII processing pipeline')).toBeInTheDocument();
  });

  it('does not render edit controls (read-only)', () => {
    const dict: EffectiveDICT = { disponibilite: 2, source: 'application' };
    renderWithRouter(<EffectiveDICTCard dict={dict} />);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
