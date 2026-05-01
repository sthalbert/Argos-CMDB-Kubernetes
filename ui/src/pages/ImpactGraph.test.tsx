import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import { ImpactSection } from './ImpactGraph';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureCluster } from '../test/fixtures';

describe('ImpactSection', () => {
  it('renders without crashing', () => {
    renderWithRouter(
      <ImpactSection entityType="clusters" entityId={fixtureCluster.id} />,
    );
    expect(screen.getByText(/impact graph/i)).toBeInTheDocument();
  });

  it('renders the root entity name on ready', async () => {
    renderWithRouter(
      <ImpactSection entityType="clusters" entityId={fixtureCluster.id} />,
    );
    await waitFor(() => {
      const matches = screen.getAllByText(fixtureCluster.name);
      expect(matches.length).toBeGreaterThan(0);
    });
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/impact/:type/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(
      <ImpactSection entityType="clusters" entityId={fixtureCluster.id} />,
    );
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
