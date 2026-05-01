import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import {
  ClusterDetail, IngressDetail, NamespaceDetail, NodeDetail,
  PodDetail, WorkloadDetail,
} from './Details';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import {
  fixtureCluster, fixtureIngress, fixtureNamespace, fixtureNode,
  fixturePod, fixtureWorkload,
} from '../test/fixtures';

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
