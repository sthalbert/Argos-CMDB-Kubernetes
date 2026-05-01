import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { IdentityCard } from './IdentityCard';

describe('IdentityCard', () => {
  it('renders without crashing with an empty rows array', () => {
    const { container } = render(<IdentityCard rows={[]} />);
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the default "Identity" section title', () => {
    const { getByText } = render(<IdentityCard rows={[]} />);
    expect(getByText('Identity')).toBeInTheDocument();
  });

  it('renders a custom title when provided', () => {
    const { getByText } = render(<IdentityCard rows={[]} title="Node Info" />);
    expect(getByText('Node Info')).toBeInTheDocument();
  });

  it('renders each row label in the kv-list', () => {
    const { getByText } = render(
      <IdentityCard
        rows={[
          { label: 'Instance type', value: 'tinav5.c4r8p1' },
          { label: 'Zone', value: 'eu-west-2a' },
        ]}
      />,
    );
    expect(getByText('Instance type')).toBeInTheDocument();
    expect(getByText('Zone')).toBeInTheDocument();
  });

  it('renders row values in the kv-list', () => {
    const { getByText } = render(
      <IdentityCard rows={[{ label: 'Provider ID', value: 'aws:///eu-west-2a/i-dead' }]} />,
    );
    expect(getByText('aws:///eu-west-2a/i-dead')).toBeInTheDocument();
  });
});
