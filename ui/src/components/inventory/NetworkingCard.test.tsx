import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { NetworkingCard } from './NetworkingCard';

describe('NetworkingCard', () => {
  it('renders without crashing with an empty rows array', () => {
    const { container } = render(<NetworkingCard rows={[]} />);
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the "Networking" section title', () => {
    const { getByText } = render(<NetworkingCard rows={[]} />);
    expect(getByText('Networking')).toBeInTheDocument();
  });

  it('renders each row label in the kv-list', () => {
    const { getByText } = render(
      <NetworkingCard
        rows={[
          { label: 'Internal IP', value: '10.0.0.1' },
          { label: 'External IP', value: '203.0.113.1' },
        ]}
      />,
    );
    expect(getByText('Internal IP')).toBeInTheDocument();
    expect(getByText('External IP')).toBeInTheDocument();
  });

  it('renders row values in the kv-list', () => {
    const { getByText } = render(
      <NetworkingCard rows={[{ label: 'Pod CIDR', value: '10.244.0.0/24' }]} />,
    );
    expect(getByText('10.244.0.0/24')).toBeInTheDocument();
  });

  it('renders children below the kv-list', () => {
    const { getByText } = render(
      <NetworkingCard rows={[]}>
        <p>extra child</p>
      </NetworkingCard>,
    );
    expect(getByText('extra child')).toBeInTheDocument();
  });
});
