import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import {
  AsyncView, Code, Dash, Empty, IdLink, KV, Labels, LayerPill,
  LoadBalancerAddresses, SectionTitle, ShortId,
} from './components';

describe('Dash / Code / Empty', () => {
  it('Dash renders an em-dash', () => {
    const { container } = render(<Dash />);
    expect(container.textContent).toBe('—');
  });

  it('Code wraps children in code.inline-code', () => {
    const { container } = render(<Code>foo</Code>);
    const el = container.querySelector('code.inline-code');
    expect(el?.textContent).toBe('foo');
  });

  it('Empty renders the message', () => {
    const { getByText } = render(<Empty message="nothing here" />);
    expect(getByText('nothing here')).toBeInTheDocument();
  });
});

describe('LayerPill', () => {
  it('renders the layer text', () => {
    const { getByText } = render(<LayerPill layer="applicative" />);
    expect(getByText('applicative')).toBeInTheDocument();
  });
});

describe('SectionTitle', () => {
  it('renders the title without a count by default', () => {
    const { getByText, queryByText } = render(
      <SectionTitle>Pods</SectionTitle>,
    );
    expect(getByText('Pods')).toBeInTheDocument();
    expect(queryByText(/\(/)).toBeNull();
  });

  it('renders a count when provided', () => {
    const { getByText } = render(<SectionTitle count={7}>Pods</SectionTitle>);
    // count renders as " (7)" inside a muted span
    expect(getByText('(7)', { exact: false })).toBeInTheDocument();
  });
});

describe('ShortId / IdLink', () => {
  it('ShortId truncates UUID to first 8 chars and adds an ellipsis', () => {
    const { container } = render(<ShortId id="abcdef0123456789" />);
    expect(container.textContent).toBe('abcdef01…');
  });

  it('ShortId renders Dash for null/undefined', () => {
    const { container } = render(<ShortId id={null} />);
    expect(container.textContent).toBe('—');
  });

  it('IdLink wraps the short id in a link with a title attr', () => {
    const { getByTitle } = render(
      <MemoryRouter>
        <IdLink to="/x" id="abcdef0123456789" />
      </MemoryRouter>,
    );
    const link = getByTitle('abcdef0123456789');
    expect(link.tagName).toBe('A');
    expect(link.textContent).toBe('abcdef01…');
  });
});

describe('KV', () => {
  it('renders label and value', () => {
    const { getByText } = render(<KV k="Owner" v="platform" />);
    expect(getByText('Owner')).toBeInTheDocument();
    expect(getByText('platform')).toBeInTheDocument();
  });

  it('renders Dash when value is empty', () => {
    const { container } = render(<KV k="Owner" v="" />);
    expect(container.querySelector('dd')?.textContent).toBe('—');
  });
});

describe('LoadBalancerAddresses', () => {
  it('renders Dash when entries is empty/null', () => {
    const { container } = render(<LoadBalancerAddresses entries={[]} />);
    expect(container.textContent).toBe('—');
  });

  it('renders an IP entry as a code element', () => {
    const { container } = render(
      <LoadBalancerAddresses entries={[{ ip: '203.0.113.1' }]} />,
    );
    const code = container.querySelector('code');
    expect(code?.textContent).toBe('203.0.113.1');
  });

  it('renders ports inline in [port/protocol] form', () => {
    const { container } = render(
      <LoadBalancerAddresses
        entries={[{ ip: '10.0.0.1', ports: [{ port: 443, protocol: 'TCP' }] }]}
      />,
    );
    expect(container.textContent).toContain('[443/TCP]');
  });

  it('defaults missing protocol to TCP', () => {
    const { container } = render(
      <LoadBalancerAddresses entries={[{ ip: '10.0.0.1', ports: [{ port: 80 }] }]} />,
    );
    expect(container.textContent).toContain('[80/TCP]');
  });
});

describe('Labels', () => {
  it('renders Dash for null/empty labels', () => {
    expect(render(<Labels labels={null} />).container.textContent).toBe('—');
    expect(render(<Labels labels={{}} />).container.textContent).toBe('—');
  });

  it('renders one chip per label', () => {
    const { container } = render(<Labels labels={{ env: 'prod', tier: 'web' }} />);
    expect(container.querySelectorAll('.label-chip').length).toBe(2);
  });
});

describe('AsyncView', () => {
  it('renders loading state', () => {
    const { container } = render(
      <AsyncView state={{ status: 'loading' }}>{() => <div>data</div>}</AsyncView>,
    );
    // actual text: "Loading…" (with unicode ellipsis)
    expect(container.textContent).toContain('Loading');
  });

  it('renders error state with message', () => {
    const { container } = render(
      <AsyncView state={{ status: 'error', error: 'boom' }}>{() => <div>data</div>}</AsyncView>,
    );
    // actual text: "Failed to load: boom"
    expect(container.textContent).toContain('boom');
  });

  it('renders children with data on ready', () => {
    const { getByText } = render(
      <AsyncView state={{ status: 'ready', data: { name: 'x' } }}>
        {(d) => <div>name: {d.name}</div>}
      </AsyncView>,
    );
    expect(getByText('name: x')).toBeInTheDocument();
  });
});
