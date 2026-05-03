import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { PageHead } from './PageHead';

describe('PageHead', () => {
  it('renders title in lv-page-title and sub in lv-page-sub', () => {
    const { container } = render(<PageHead title="Clusters" sub="3 active" />);
    expect(container.querySelector('.lv-page-title')?.textContent).toBe('Clusters');
    expect(container.querySelector('.lv-page-sub')?.textContent).toBe('3 active');
  });

  it('renders actions in lv-page-actions', () => {
    const { container } = render(
      <PageHead title="x" actions={<button>save</button>} />,
    );
    expect(container.querySelector('.lv-page-actions button')?.textContent).toBe('save');
  });

  it('omits sub and actions when not given', () => {
    const { container } = render(<PageHead title="x" />);
    expect(container.querySelector('.lv-page-sub')).toBeNull();
    expect(container.querySelector('.lv-page-actions')).toBeNull();
  });
});
