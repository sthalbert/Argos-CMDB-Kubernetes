import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { Tabs } from './Tabs';

const items = [
  { key: 'overview', label: 'Overview' },
  { key: 'workloads', label: 'Workloads' },
];

describe('Tabs', () => {
  it('renders all tabs and marks the active one', () => {
    const { container } = render(<Tabs items={items} active="workloads" onChange={() => {}} />);
    const tabs = container.querySelectorAll('button.lv-tab');
    expect(tabs.length).toBe(2);
    expect(tabs[1].classList.contains('active')).toBe(true);
    expect(tabs[0].classList.contains('active')).toBe(false);
  });

  it('calls onChange with the clicked key', () => {
    const onChange = vi.fn();
    const { getByText } = render(<Tabs items={items} active="overview" onChange={onChange} />);
    fireEvent.click(getByText('Workloads'));
    expect(onChange).toHaveBeenCalledWith('workloads');
  });

  it('exposes role="tab" for a11y', () => {
    const { container } = render(<Tabs items={items} active="overview" onChange={() => {}} />);
    const tabs = container.querySelectorAll('[role="tab"]');
    expect(tabs.length).toBe(2);
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');
    expect(tabs[1].getAttribute('aria-selected')).toBe('false');
  });
});
