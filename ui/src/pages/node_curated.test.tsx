import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { NodeCuratedCard } from './node_curated';
import { fixtureNode, fixtureMe } from '../test/fixtures';
import { MeProvider } from '../me';

describe('node_curated', () => {
  it('renders without crashing', () => {
    const { container } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <NodeCuratedCard node={fixtureNode} onSaved={() => {}} />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the owner field when populated', () => {
    const { getByText } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <NodeCuratedCard
            node={{ ...fixtureNode, owner: 'infra-team' }}
            onSaved={() => {}}
          />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(getByText('infra-team')).toBeInTheDocument();
  });
});
