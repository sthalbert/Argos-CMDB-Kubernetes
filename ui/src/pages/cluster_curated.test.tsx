import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClusterCuratedCard } from './cluster_curated';
import { fixtureCluster, fixtureMe } from '../test/fixtures';
import { MeProvider } from '../me';

describe('cluster_curated', () => {
  it('renders without crashing', () => {
    const { container } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <ClusterCuratedCard cluster={fixtureCluster} onSaved={() => {}} />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the owner field when populated', () => {
    const { getByText } = render(
      <MemoryRouter>
        <MeProvider value={fixtureMe}>
          <ClusterCuratedCard
            cluster={{ ...fixtureCluster, owner: 'sre-team' }}
            onSaved={() => {}}
          />
        </MeProvider>
      </MemoryRouter>,
    );
    expect(getByText('sre-team')).toBeInTheDocument();
  });
});
