import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { KvList } from './KvList';

describe('KvList', () => {
  it('renders dt/dd pairs in a kv-list dl', () => {
    const { container } = render(
      <KvList items={[['name', 'frodo'], ['region', 'shire']]} />,
    );
    const dl = container.querySelector('dl.kv-list')!;
    expect(dl).toBeTruthy();
    const dts = dl.querySelectorAll('dt');
    const dds = dl.querySelectorAll('dd');
    expect(dts.length).toBe(2);
    expect(dds.length).toBe(2);
    expect(dts[0].textContent).toBe('name');
    expect(dds[1].textContent).toBe('shire');
  });

  it('renders nothing for an empty items array', () => {
    const { container } = render(<KvList items={[]} />);
    expect(container.querySelector('dt')).toBeNull();
  });
});
