import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen } from '@testing-library/react';
import { ClusterPolicies, PolicyReports } from './Policies';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

const disabledProblem = {
  title: 'policies disabled',
  status: 409,
  detail: 'enable policies_enabled in admin settings to use this endpoint',
};

describe('Policies feature-disabled banner', () => {
  it('ClusterPolicies shows the banner when the API answers 409', async () => {
    server.use(
      http.get('/v1/cluster-policies', () =>
        HttpResponse.json(disabledProblem, { status: 409 }),
      ),
    );
    renderWithRouter(<ClusterPolicies />);
    await screen.findByText(/feature is disabled/i);
    expect(screen.getByText('policies_enabled')).toBeInTheDocument();
  });

  it('PolicyReports shows the banner when the API answers 409', async () => {
    server.use(
      http.get('/v1/policy-reports', () =>
        HttpResponse.json(disabledProblem, { status: 409 }),
      ),
    );
    renderWithRouter(<PolicyReports />);
    await screen.findByText(/feature is disabled/i);
  });

  it('ClusterPolicies keeps the generic error for non-409 failures', async () => {
    server.use(
      http.get('/v1/cluster-policies', () =>
        HttpResponse.json({ title: 'boom', status: 500 }, { status: 500 }),
      ),
    );
    renderWithRouter(<ClusterPolicies />);
    await screen.findByText(/failed to load/i);
    expect(screen.queryByText(/feature is disabled/i)).toBeNull();
  });
});
