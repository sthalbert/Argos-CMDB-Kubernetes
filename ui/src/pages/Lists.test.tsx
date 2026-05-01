import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import {
  Clusters,
  Ingresses,
  Namespaces,
  Nodes,
  PersistentVolumeClaims,
  PersistentVolumes,
  Pods,
  Services,
  Workloads,
} from './Lists';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import {
  fixtureCluster,
  fixtureIngress,
  fixtureNamespace,
  fixtureNode,
  fixturePV,
  fixturePVC,
  fixturePod,
  fixtureService,
  fixtureWorkload,
} from '../test/fixtures';

// ---------------------------------------------------------------------------
// Clusters
// ---------------------------------------------------------------------------
describe('Clusters list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Clusters />, { initialPath: '/clusters' });
    expect(screen.getByRole('heading', { name: /clusters/i })).toBeInTheDocument();
  });

  it('shows loading then renders the cluster display name', async () => {
    renderWithRouter(<Clusters />, { initialPath: '/clusters' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureCluster.display_name!)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/clusters', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Clusters />, { initialPath: '/clusters' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Nodes
// ---------------------------------------------------------------------------
describe('Nodes list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Nodes />, { initialPath: '/nodes' });
    expect(screen.getByRole('heading', { name: /nodes/i })).toBeInTheDocument();
  });

  it('shows loading then renders the node name', async () => {
    renderWithRouter(<Nodes />, { initialPath: '/nodes' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureNode.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/nodes', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Nodes />, { initialPath: '/nodes' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Namespaces
// ---------------------------------------------------------------------------
describe('Namespaces list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Namespaces />, { initialPath: '/namespaces' });
    expect(screen.getByRole('heading', { name: /namespaces/i })).toBeInTheDocument();
  });

  it('shows loading then renders the namespace name', async () => {
    renderWithRouter(<Namespaces />, { initialPath: '/namespaces' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureNamespace.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/namespaces', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Namespaces />, { initialPath: '/namespaces' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Workloads
// ---------------------------------------------------------------------------
describe('Workloads list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Workloads />, { initialPath: '/workloads' });
    expect(screen.getByRole('heading', { name: /workloads/i })).toBeInTheDocument();
  });

  it('shows loading then renders the workload name', async () => {
    renderWithRouter(<Workloads />, { initialPath: '/workloads' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureWorkload.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/workloads', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Workloads />, { initialPath: '/workloads' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Pods
// ---------------------------------------------------------------------------
describe('Pods list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Pods />, { initialPath: '/pods' });
    expect(screen.getByRole('heading', { name: /pods/i })).toBeInTheDocument();
  });

  it('shows loading then renders the pod name', async () => {
    renderWithRouter(<Pods />, { initialPath: '/pods' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixturePod.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/pods', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Pods />, { initialPath: '/pods' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Services
// ---------------------------------------------------------------------------
describe('Services list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Services />, { initialPath: '/services' });
    expect(screen.getByRole('heading', { name: /services/i })).toBeInTheDocument();
  });

  it('shows loading then renders the service name', async () => {
    renderWithRouter(<Services />, { initialPath: '/services' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureService.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/services', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Services />, { initialPath: '/services' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// Ingresses
// ---------------------------------------------------------------------------
describe('Ingresses list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<Ingresses />, { initialPath: '/ingresses' });
    expect(screen.getByRole('heading', { name: /ingresses/i })).toBeInTheDocument();
  });

  it('shows loading then renders the ingress name', async () => {
    renderWithRouter(<Ingresses />, { initialPath: '/ingresses' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixtureIngress.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/ingresses', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<Ingresses />, { initialPath: '/ingresses' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// PersistentVolumes
// ---------------------------------------------------------------------------
describe('PersistentVolumes list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<PersistentVolumes />, { initialPath: '/persistent-volumes' });
    expect(screen.getByRole('heading', { name: /persistent volumes/i })).toBeInTheDocument();
  });

  it('shows loading then renders the PV name', async () => {
    renderWithRouter(<PersistentVolumes />, { initialPath: '/persistent-volumes' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixturePV.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/persistentvolumes', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<PersistentVolumes />, { initialPath: '/persistent-volumes' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});

// ---------------------------------------------------------------------------
// PersistentVolumeClaims
// ---------------------------------------------------------------------------
describe('PersistentVolumeClaims list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<PersistentVolumeClaims />, { initialPath: '/persistent-volume-claims' });
    expect(
      screen.getByRole('heading', { name: /persistent volume claims/i }),
    ).toBeInTheDocument();
  });

  it('shows loading then renders the PVC name', async () => {
    renderWithRouter(<PersistentVolumeClaims />, { initialPath: '/persistent-volume-claims' });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(fixturePVC.name)).toBeInTheDocument(),
    );
  });

  it('shows error state on 500', async () => {
    server.use(
      http.get('/v1/persistentvolumeclaims', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<PersistentVolumeClaims />, { initialPath: '/persistent-volume-claims' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
