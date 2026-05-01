import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { LabelsCard } from './LabelsCard';

describe('LabelsCard', () => {
  it('renders without crashing with no labels', () => {
    const { container } = render(<LabelsCard />);
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the default "Labels" section title', () => {
    const { getByText } = render(<LabelsCard />);
    expect(getByText('Labels')).toBeInTheDocument();
  });

  it('renders a custom title when provided', () => {
    const { getByText } = render(<LabelsCard title="Cloud Tags" />);
    expect(getByText('Cloud Tags')).toBeInTheDocument();
  });

  it('renders a dash when labels is null', () => {
    const { container } = render(<LabelsCard labels={null} />);
    expect(container.textContent).toContain('—');
  });

  it('renders a dash when labels is an empty object', () => {
    const { container } = render(<LabelsCard labels={{}} />);
    expect(container.textContent).toContain('—');
  });

  it('renders one chip per label when populated', () => {
    const { container } = render(
      <LabelsCard labels={{ env: 'prod', tier: 'web' }} />,
    );
    expect(container.querySelectorAll('.label-chip').length).toBe(2);
  });
});
