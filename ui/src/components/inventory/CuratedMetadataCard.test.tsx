import { describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import { CuratedMetadataCard } from './CuratedMetadataCard';
import type { CuratedValues } from './CuratedMetadataCard';
import { MeProvider } from '../../me';
import { fixtureMe } from '../../test/fixtures';

const adminMe = { ...fixtureMe }; // already admin
const viewerMe = { ...fixtureMe, role: 'viewer' as const, scopes: ['read'] as string[] };

const noopSave = vi.fn().mockResolvedValue(undefined);
const noopSaved = vi.fn();

const emptyValues: CuratedValues = {
  owner: null,
  criticality: null,
  notes: null,
  runbook_url: null,
  annotations: null,
};

const populatedValues: CuratedValues = {
  owner: 'team-platform',
  criticality: 'high',
  notes: 'Critical cluster — notify oncall before maintenance.',
  runbook_url: 'https://runbooks.example.com/prod',
  annotations: { 'longue-vue.io/env': 'prod' },
};

describe('CuratedMetadataCard', () => {
  it('renders without crashing with empty values (viewer)', () => {
    const { container } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the default "Ownership & context" heading', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('Ownership & context')).toBeInTheDocument();
  });

  it('renders a custom title when provided', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard
          values={emptyValues}
          onSave={noopSave}
          onSaved={noopSaved}
          title="VM context"
        />
      </MeProvider>,
    );
    expect(getByText('VM context')).toBeInTheDocument();
  });

  it('renders viewer empty message when empty and role is viewer', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('No curated metadata recorded.')).toBeInTheDocument();
  });

  it('renders the Edit button for admin role', () => {
    const { getByRole } = render(
      <MeProvider value={adminMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByRole('button', { name: 'Edit' })).toBeInTheDocument();
  });

  it('does not render Edit button for viewer role', () => {
    const { queryByRole } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(queryByRole('button', { name: 'Edit' })).toBeNull();
  });

  it('renders owner value when populated', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={populatedValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(getByText('team-platform')).toBeInTheDocument();
  });

  it('renders criticality as a pill when populated', () => {
    const { getByText } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={populatedValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    const pill = getByText('high');
    expect(pill.className).toContain('pill');
  });

  it('renders the runbook URL as a link when populated', () => {
    const { getByRole } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={populatedValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    const link = getByRole('link');
    expect(link).toHaveAttribute('href', 'https://runbooks.example.com/prod');
  });

  it('renders notes in a pre element when populated', () => {
    const { container } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={populatedValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    const pre = container.querySelector('pre.curated-notes');
    expect(pre?.textContent).toContain('Critical cluster');
  });

  it('renders annotation chips when annotations are populated', () => {
    const { container } = render(
      <MeProvider value={viewerMe}>
        <CuratedMetadataCard values={populatedValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(container.querySelectorAll('.label-chip').length).toBeGreaterThan(0);
  });

  it('renders empty hint for admin when no curated metadata exists', () => {
    const { container } = render(
      <MeProvider value={adminMe}>
        <CuratedMetadataCard values={emptyValues} onSave={noopSave} onSaved={noopSaved} />
      </MeProvider>,
    );
    expect(container.textContent).toContain('No curated metadata yet');
  });

  it('renders a custom emptyHint when provided', () => {
    const { getByText } = render(
      <MeProvider value={adminMe}>
        <CuratedMetadataCard
          values={emptyValues}
          onSave={noopSave}
          onSaved={noopSaved}
          emptyHint="Add ownership info for this VM."
        />
      </MeProvider>,
    );
    expect(getByText('Add ownership info for this VM.')).toBeInTheDocument();
  });
});
