import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { NamespaceCuratedCard } from './namespace_curated';
import { fixtureNamespace, fixtureMe } from '../test/fixtures';
import { MeProvider } from '../me';

describe('namespace_curated', () => {
  it('renders without crashing', () => {
    const { container } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <NamespaceCuratedCard namespace={fixtureNamespace} onSaved={() => {}} />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the owner field when populated', () => {
    const { getByText } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <NamespaceCuratedCard
            namespace={{ ...fixtureNamespace, owner: 'payments-team' }}
            onSaved={() => {}}
          />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(getByText('payments-team')).toBeInTheDocument();
  });
});
