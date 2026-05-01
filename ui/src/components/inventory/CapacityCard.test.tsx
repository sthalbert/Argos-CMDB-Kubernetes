import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { CapacityCard } from './CapacityCard';
import type { CapacityRow } from './CapacityCard';

const cpuRow: CapacityRow = { dimension: 'CPU', capacity: '4', allocatable: '3800m' };
const memRow: CapacityRow = { dimension: 'Memory', capacity: '8Gi', allocatable: '7.5Gi' };

describe('CapacityCard', () => {
  it('renders without crashing with an empty rows array', () => {
    const { container } = render(<CapacityCard rows={[]} />);
    expect(container.firstChild).not.toBeNull();
  });

  it('renders "Resources" section title when showAllocatable is true (default)', () => {
    const { getByText } = render(<CapacityCard rows={[cpuRow]} />);
    expect(getByText('Resources')).toBeInTheDocument();
  });

  it('renders "Capacity" section title when showAllocatable is false', () => {
    const { container } = render(<CapacityCard rows={[cpuRow]} showAllocatable={false} />);
    const sectionTitle = container.querySelector('h3.section-title');
    expect(sectionTitle?.textContent).toBe('Capacity');
  });

  it('renders dimension names and values in the table', () => {
    const { getByText } = render(<CapacityCard rows={[cpuRow, memRow]} />);
    expect(getByText('CPU')).toBeInTheDocument();
    expect(getByText('4')).toBeInTheDocument();
    expect(getByText('3800m')).toBeInTheDocument();
    expect(getByText('Memory')).toBeInTheDocument();
  });

  it('renders Allocatable column header when showAllocatable is true', () => {
    const { getByText } = render(<CapacityCard rows={[cpuRow]} />);
    expect(getByText('Allocatable')).toBeInTheDocument();
  });

  it('does not render Allocatable column when showAllocatable is false', () => {
    const { queryByText } = render(<CapacityCard rows={[cpuRow]} showAllocatable={false} />);
    expect(queryByText('Allocatable')).toBeNull();
  });

  it('renders a dash for a null capacity value', () => {
    const row: CapacityRow = { dimension: 'Ephemeral Storage', capacity: null };
    const { container } = render(<CapacityCard rows={[row]} showAllocatable={false} />);
    // The Dash component renders an em-dash
    expect(container.textContent).toContain('—');
  });

  it('renders the emptyMessage when all rows have no values', () => {
    const emptyRows: CapacityRow[] = [{ dimension: 'CPU', capacity: null, allocatable: null }];
    const { getByText } = render(
      <CapacityCard rows={emptyRows} emptyMessage="No capacity data available." />,
    );
    expect(getByText('No capacity data available.')).toBeInTheDocument();
  });

  it('does not show emptyMessage when rows have values', () => {
    const { queryByText } = render(
      <CapacityCard rows={[cpuRow]} emptyMessage="No capacity data available." />,
    );
    expect(queryByText('No capacity data available.')).toBeNull();
  });
});
