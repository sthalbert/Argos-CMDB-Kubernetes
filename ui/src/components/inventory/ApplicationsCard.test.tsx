import { describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import { ApplicationsCard } from './ApplicationsCard';
import { MeProvider } from '../../me';
import type { VMApplication } from '../../api';
import { fixtureMe } from '../../test/fixtures';

const adminMe = { ...fixtureMe }; // already admin
const viewerMe = { ...fixtureMe, role: 'viewer' as const, scopes: ['read'] as string[] };

const noopSave = vi.fn().mockResolvedValue(undefined);
const noopSaved = vi.fn();

const fixtureApps: VMApplication[] = [
  {
    product: 'vault',
    version: '1.15.4',
    name: 'vault-prod-01',
    notes: null,
    added_at: '2025-01-01T00:00:00Z',
    added_by: 'alice',
  },
  {
    product: 'nginx',
    version: '1.27.0',
    added_at: '2025-01-02T00:00:00Z',
    added_by: 'bob',
  },
];

describe('ApplicationsCard', () => {
  it('renders without crashing with no applications (viewer)', () => {
    const { container } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={null} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the Applications section heading', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={[]} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('Applications')).toBeInTheDocument();
  });

  it('renders empty message for viewer when list is empty', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={[]} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('No applications declared.')).toBeInTheDocument();
  });

  it('renders edit-hint empty message for admin when list is empty', () => {
    const { getByText } = render(
      <MeProvider value={adminMe}>
        <ApplicationsCard applications={[]} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    // The admin branch shows a longer hint mentioning the EOL scanner — viewer only sees 'No applications declared.'
    expect(getByText(/EOL scanner uses this to track product lifecycle/)).toBeInTheDocument();
  });

  it('renders the Edit button for admin role', () => {
    const { getByRole } = render(
      <MeProvider value={adminMe}>
        <ApplicationsCard applications={[]} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByRole('button', { name: 'Edit' })).toBeInTheDocument();
  });

  it('does not render Edit button for viewer role', () => {
    const { queryByRole } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={[]} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(queryByRole('button', { name: 'Edit' })).toBeNull();
  });

  it('renders product and version in a table when applications are populated', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={fixtureApps} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('vault')).toBeInTheDocument();
    expect(getByText('1.15.4')).toBeInTheDocument();
    expect(getByText('nginx')).toBeInTheDocument();
    expect(getByText('1.27.0')).toBeInTheDocument();
  });

  it('renders instance name when present and dash when absent', () => {
    const { getByText, container } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={fixtureApps} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    // First app has name 'vault-prod-01', second has no name → Dash
    expect(getByText('vault-prod-01')).toBeInTheDocument();
    expect(container.textContent).toContain('—');
  });

  it('renders added_by value in the table', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <ApplicationsCard applications={fixtureApps} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText(/alice/)).toBeInTheDocument();
  });
});
