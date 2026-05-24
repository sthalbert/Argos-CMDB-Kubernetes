import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  ClusterDetail, IngressDetail, NamespaceDetail, NodeDetail,
  PersistentVolumeClaimDetail, PodDetail, ServiceDetail, WorkloadDetail,
} from './Details';
import { OriginLine } from '../components/OriginLine';
import { renderWithRouter } from '../test/render';
import { MeProvider } from '../me';
import type { Me } from '../api';
import { server } from '../test/server';
import {
  fixtureApplication, fixtureCluster, fixtureIngress, fixtureMe, fixtureNamespace,
  fixtureNode, fixturePod, fixturePVC, fixtureService, fixtureWorkload,
} from '../test/fixtures';

// renderWorkload mounts WorkloadDetail under both a MemoryRouter (the page
// uses Links) and a MeProvider so we can exercise the role-gated
// ApplicationCard affordances.
function renderWorkload(me: Me | null) {
  return render(
    <MeProvider value={me}>
      <MemoryRouter initialEntries={[`/workloads/${fixtureWorkload.id}`]}>
        <Routes>
          <Route path="/workloads/:id" element={<WorkloadDetail />} />
        </Routes>
      </MemoryRouter>
    </MeProvider>,
  );
}

const viewerMe: Me = { ...fixtureMe, role: 'viewer', scopes: ['read'] };

describe('ClusterDetail', () => {
  it('renders without crashing', () => {
    renderWithRouter(<ClusterDetail />, {
      initialPath: `/clusters/${fixtureCluster.id}`,
      routePath: '/clusters/:id',
    });
    // Multiple Loading… elements may appear (AsyncView + ImpactSection)
    expect(screen.getAllByText(/loading/i).length).toBeGreaterThan(0);
  });

  it('renders the cluster name on ready', async () => {
    renderWithRouter(<ClusterDetail />, {
      initialPath: `/clusters/${fixtureCluster.id}`,
      routePath: '/clusters/:id',
    });
    // ClusterDetail renders display_name || name; fixture has display_name 'Prod EU West'
    await waitFor(() =>
      expect(screen.getByText(fixtureCluster.display_name!)).toBeInTheDocument(),
    );
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/clusters/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<ClusterDetail />, {
      initialPath: `/clusters/${fixtureCluster.id}`,
      routePath: '/clusters/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

describe('NamespaceDetail', () => {
  it('renders the namespace name on ready', async () => {
    renderWithRouter(<NamespaceDetail />, {
      initialPath: `/namespaces/${fixtureNamespace.id}`,
      routePath: '/namespaces/:id',
    });
    // "payments" appears in multiple places; check the h2 heading specifically
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureNamespace.name) }),
      ).toBeInTheDocument(),
    );
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/namespaces/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<NamespaceDetail />, {
      initialPath: `/namespaces/${fixtureNamespace.id}`,
      routePath: '/namespaces/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

describe('WorkloadDetail', () => {
  it('renders the workload name on ready', async () => {
    renderWithRouter(<WorkloadDetail />, {
      initialPath: `/workloads/${fixtureWorkload.id}`,
      routePath: '/workloads/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureWorkload.name)).toBeInTheDocument(),
    );
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/workloads/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<WorkloadDetail />, {
      initialPath: `/workloads/${fixtureWorkload.id}`,
      routePath: '/workloads/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('renders the ApplicationCard', async () => {
    renderWorkload(fixtureMe);
    await waitFor(() =>
      expect(screen.getByTestId('application-card')).toBeInTheDocument(),
    );
    // Unlinked fixture → the card shows "Not linked".
    expect(screen.getByText('Not linked')).toBeInTheDocument();
  });

  it('shows the linked application name from the denormalized field', async () => {
    server.use(
      http.get('/v1/workloads/:id', () =>
        HttpResponse.json({
          ...fixtureWorkload,
          application_id: fixtureApplication.id,
          application_name: fixtureApplication.name,
        }),
      ),
    );
    renderWorkload(fixtureMe);
    const card = await screen.findByTestId('application-card');
    expect(within(card).getByText(fixtureApplication.name)).toBeInTheDocument();
  });

  it('lets an editor link a workload to an application', async () => {
    let patchedBody: unknown = null;
    server.use(
      http.patch('/v1/workloads/:id', async ({ request }) => {
        patchedBody = await request.json();
        return HttpResponse.json({
          ...fixtureWorkload,
          application_id: fixtureApplication.id,
          application_name: fixtureApplication.name,
        });
      }),
    );
    renderWorkload(fixtureMe);
    const card = await screen.findByTestId('application-card');
    // Editor sees the Link… button.
    await userEvent.click(within(card).getByRole('button', { name: /link/i }));
    await userEvent.type(
      within(card).getByLabelText('Search applications'),
      'bill',
    );
    const pick = await within(card).findByRole('button', { name: /billing/i });
    await userEvent.click(pick);
    await waitFor(() =>
      expect(patchedBody).toEqual({ application_id: fixtureApplication.id }),
    );
  });

  it('hides edit affordances from a viewer', async () => {
    renderWorkload(viewerMe);
    const card = await screen.findByTestId('application-card');
    expect(within(card).queryByRole('button', { name: /link/i })).toBeNull();
    expect(within(card).queryByRole('button', { name: /change/i })).toBeNull();
  });
});

describe('PodDetail', () => {
  it('renders the pod name on ready', async () => {
    renderWithRouter(<PodDetail />, {
      initialPath: `/pods/${fixturePod.id}`,
      routePath: '/pods/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixturePod.name)).toBeInTheDocument(),
    );
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/pods/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<PodDetail />, {
      initialPath: `/pods/${fixturePod.id}`,
      routePath: '/pods/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

describe('NodeDetail', () => {
  it('renders the node name on ready', async () => {
    renderWithRouter(<NodeDetail />, {
      initialPath: `/nodes/${fixtureNode.id}`,
      routePath: '/nodes/:id',
    });
    // NodeDetail renders display_name || name in the h2; fixture has display_name null
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureNode.name) }),
      ).toBeInTheDocument(),
    );
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/nodes/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<NodeDetail />, {
      initialPath: `/nodes/${fixtureNode.id}`,
      routePath: '/nodes/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

describe('IngressDetail', () => {
  it('renders the ingress name on ready', async () => {
    renderWithRouter(<IngressDetail />, {
      initialPath: `/ingresses/${fixtureIngress.id}`,
      routePath: '/ingresses/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureIngress.name)).toBeInTheDocument(),
    );
  });

  // Regression for ADR-0027: IngressDetail rendered the namespace as a raw
  // UUID instead of consuming the denormalized `namespace_name` field on
  // the response payload. The bug surfaced as "I look at an ingress on
  // prod and the namespace shows as a UUID" — the list view was fixed by
  // the original ADR PR, but the detail view was missed.
  it('renders the namespace name (not a UUID) from denormalized field', async () => {
    renderWithRouter(<IngressDetail />, {
      initialPath: `/ingresses/${fixtureIngress.id}`,
      routePath: '/ingresses/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureIngress.name)).toBeInTheDocument(),
    );
    // The denormalized namespace_name should appear as a link.
    const nsLinks = screen.getAllByRole('link', { name: fixtureNamespace.name });
    expect(nsLinks.length).toBeGreaterThan(0);
    // The raw UUID (or its first 8 chars) must NOT leak through anywhere.
    expect(
      screen.queryByText(new RegExp(fixtureNamespace.id.slice(0, 8))),
    ).toBeNull();
  });

  it('renders the error state on 500', async () => {
    server.use(
      http.get('/v1/ingresses/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<IngressDetail />, {
      initialPath: `/ingresses/${fixtureIngress.id}`,
      routePath: '/ingresses/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// Parallel regression coverage for the other detail pages that suffered
// the same "UUID on parent" symptom. Each asserts that the denormalized
// parent name (namespace_name and/or cluster_name from ADR-0027) is
// rendered and that the parent's UUID does not leak through.
describe('ServiceDetail', () => {
  it('renders the namespace name (not a UUID) from denormalized field', async () => {
    renderWithRouter(<ServiceDetail />, {
      initialPath: `/services/${fixtureService.id}`,
      routePath: '/services/:id',
    });
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureService.name) }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getAllByRole('link', { name: fixtureNamespace.name }).length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByText(new RegExp(fixtureNamespace.id.slice(0, 8))),
    ).toBeNull();
  });
});

describe('PersistentVolumeClaimDetail', () => {
  it('renders the namespace name (not a UUID) from denormalized field', async () => {
    renderWithRouter(<PersistentVolumeClaimDetail />, {
      initialPath: `/persistentvolumeclaims/${fixturePVC.id}`,
      routePath: '/persistentvolumeclaims/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixturePVC.name)).toBeInTheDocument(),
    );
    expect(
      screen.getAllByRole('link', { name: fixtureNamespace.name }).length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByText(new RegExp(fixtureNamespace.id.slice(0, 8))),
    ).toBeNull();
  });
});

describe('NodeDetail (ADR-0027 extension)', () => {
  it('renders the cluster name (not a UUID) from denormalized field', async () => {
    renderWithRouter(<NodeDetail />, {
      initialPath: `/nodes/${fixtureNode.id}`,
      routePath: '/nodes/:id',
    });
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureNode.name) }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getAllByRole('link', { name: fixtureCluster.name }).length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByText(new RegExp(fixtureCluster.id.slice(0, 8))),
    ).toBeNull();
  });
});

describe('OriginLine', () => {
  it('renders origin ref when resolved', () => {
    render(
      <OriginLine
        image="local.example.com/containers/sthalbert/longue-vue-collector:0.26"
        info={{
          latest_tag: '0.27',
          is_behind: true,
          last_checked_at: '2026-05-23T10:00:00Z',
          origin_image_repo: 'ghcr.io/sthalbert/longue-vue-collector',
          origin_status: 'resolved',
        }}
      />,
    );
    expect(
      screen.getByText('ghcr.io/sthalbert/longue-vue-collector:0.26'),
    ).toBeInTheDocument();
  });

  it('renders muted "origin: unknown" with reason on hover when unresolved', () => {
    render(
      <OriginLine
        image="local.example.com/x/y:1.0.0"
        info={{
          origin_status: 'unresolved',
          origin_error: 'missing_annotation',
        }}
      />,
    );
    const unknown = screen.getByText(/origin: unknown/i);
    expect(unknown).toBeInTheDocument();
    // The title attribute is set on the muted container — find it via the
    // closest element with a title attribute.
    expect(unknown.closest('[title]')).toHaveAttribute('title', 'missing_annotation');
  });

  it('renders nothing when origin fields absent (passthrough)', () => {
    const { container } = render(
      <OriginLine
        image="nginx:1.25.3"
        info={{ latest_tag: '1.27.4', is_behind: true, last_checked_at: '2026-05-23T10:00:00Z' }}
      />,
    );
    expect(container.textContent).not.toMatch(/origin:/i);
  });
});
