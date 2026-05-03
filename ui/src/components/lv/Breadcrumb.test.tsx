import { describe, it, expect } from 'vitest';
import { renderWithRouter } from '../../test/render';
import { Breadcrumb } from './Breadcrumb';

describe('Breadcrumb', () => {
  it('renders parts separated by /', () => {
    const { container } = renderWithRouter(
      <Breadcrumb parts={[{ label: 'Clusters', to: '/clusters' }, { label: 'prod-eu' }]} />,
    );
    const root = container.querySelector('.breadcrumb')!;
    expect(root.textContent).toContain('Clusters');
    expect(root.textContent).toContain('prod-eu');
    expect(root.querySelectorAll('.breadcrumb-sep').length).toBe(1);
  });

  it('renders parts with `to` as links', () => {
    const { container } = renderWithRouter(
      <Breadcrumb parts={[{ label: 'Home', to: '/' }, { label: 'Now' }]} />,
    );
    const links = container.querySelectorAll('a');
    expect(links.length).toBe(1);
    expect(links[0].getAttribute('href')).toBe('/');
  });

  it('forwards ariaLabel to the rendered anchor', () => {
    const { container } = renderWithRouter(
      <Breadcrumb
        parts={[{ label: 'Clusters', to: '/clusters', ariaLabel: 'Back to clusters' }, { label: 'prod-eu' }]}
      />,
    );
    const link = container.querySelector('a');
    expect(link).not.toBeNull();
    expect(link!.getAttribute('aria-label')).toBe('Back to clusters');
  });
});
