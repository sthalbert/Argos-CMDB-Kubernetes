import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/react';
import { useColumnFilters } from './column_filters';

// 3-column test table with three rows. Phase has two distinct values
// (running / pending), Cluster has three (a / b / c).
function Harness({ storageKey = 'test.cf' }: { storageKey?: string }) {
  const tableRef = useColumnFilters(storageKey);
  return (
    <table ref={tableRef}>
      <thead>
        <tr>
          <th>Name</th>
          <th>Phase</th>
          <th>Cluster</th>
          <th style={{ textAlign: 'right' }}>Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr data-testid="r1">
          <td>alpha</td>
          <td>running</td>
          <td>a</td>
          <td>
            <button>edit</button>
          </td>
        </tr>
        <tr data-testid="r2">
          <td>beta</td>
          <td>pending</td>
          <td>b</td>
          <td>
            <button>edit</button>
          </td>
        </tr>
        <tr data-testid="r3">
          <td>gamma</td>
          <td>running</td>
          <td>c</td>
          <td>
            <button>edit</button>
          </td>
        </tr>
      </tbody>
    </table>
  );
}

beforeEach(() => {
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  // Any leftover popover should be removed automatically by hook cleanup;
  // belt and braces in case a test fails mid-way.
  document
    .querySelectorAll('.col-filter-popover')
    .forEach((el) => el.remove());
  vi.restoreAllMocks();
});

function getRow(container: HTMLElement, id: string) {
  return container.querySelector<HTMLTableRowElement>(
    `[data-testid="${id}"]`,
  )!;
}

describe('useColumnFilters', () => {
  it('adds a filter button to every non-action header', () => {
    const { container } = render(<Harness />);
    const ths = container.querySelectorAll('thead > tr > th');
    expect(ths[0].querySelector('.col-filter-btn')).not.toBeNull();
    expect(ths[1].querySelector('.col-filter-btn')).not.toBeNull();
    expect(ths[2].querySelector('.col-filter-btn')).not.toBeNull();
    // Right-aligned action column is skipped.
    expect(ths[3].querySelector('.col-filter-btn')).toBeNull();
  });

  it('opens a popover with the column\'s distinct values when the funnel is clicked', () => {
    const { container } = render(<Harness />);
    const phaseBtn = container
      .querySelectorAll('thead > tr > th')[1]
      .querySelector('.col-filter-btn') as HTMLElement;
    fireEvent.click(phaseBtn);
    const pop = document.querySelector('.col-filter-popover')!;
    const labels = Array.from(
      pop.querySelectorAll<HTMLElement>('.col-filter-popover-label'),
    ).map((el) => el.textContent);
    expect(labels.sort()).toEqual(['pending', 'running']);
  });

  it('hides rows whose value is unchecked and persists the selection', () => {
    const { container } = render(<Harness />);
    const phaseBtn = container
      .querySelectorAll('thead > tr > th')[1]
      .querySelector('.col-filter-btn') as HTMLElement;
    fireEvent.click(phaseBtn);
    const pop = document.querySelector('.col-filter-popover')!;
    const checkboxes = Array.from(
      pop.querySelectorAll<HTMLInputElement>('input[type=checkbox]'),
    );
    // Find the "pending" checkbox (sibling label text)
    const pendingCb = checkboxes.find(
      (cb) =>
        cb.parentElement?.querySelector('.col-filter-popover-label')
          ?.textContent === 'pending',
    )!;
    fireEvent.click(pendingCb);

    expect(getRow(container, 'r1').style.display).toBe('');
    expect(getRow(container, 'r2').style.display).toBe('none');
    expect(getRow(container, 'r3').style.display).toBe('');

    const saved = JSON.parse(localStorage.getItem('lv.col-filters.test.cf')!);
    expect(saved['1']).toEqual(['running']);

    // Funnel button now shows active state.
    expect(phaseBtn.classList.contains('col-filter-active')).toBe(true);
  });

  it('restores hidden rows on mount when a stored filter exists', () => {
    localStorage.setItem(
      'lv.col-filters.test.cf',
      JSON.stringify({ '2': ['a'] }),
    );
    const { container } = render(<Harness />);
    expect(getRow(container, 'r1').style.display).toBe('');
    expect(getRow(container, 'r2').style.display).toBe('none');
    expect(getRow(container, 'r3').style.display).toBe('none');
  });

  it('"All" reverts to no filter and unhides every row', () => {
    localStorage.setItem(
      'lv.col-filters.test.cf',
      JSON.stringify({ '1': ['running'] }),
    );
    const { container } = render(<Harness />);
    expect(getRow(container, 'r2').style.display).toBe('none');
    const phaseBtn = container
      .querySelectorAll('thead > tr > th')[1]
      .querySelector('.col-filter-btn') as HTMLElement;
    fireEvent.click(phaseBtn);
    const allBtn = document.querySelector(
      '.col-filter-popover-actions .link-btn',
    ) as HTMLButtonElement;
    fireEvent.click(allBtn);
    expect(getRow(container, 'r1').style.display).toBe('');
    expect(getRow(container, 'r2').style.display).toBe('');
    expect(getRow(container, 'r3').style.display).toBe('');
    expect(localStorage.getItem('lv.col-filters.test.cf')).toBeNull();
  });

  it('cleans up popover on unmount and restores all row visibility', () => {
    const { container, unmount } = render(<Harness />);
    const phaseBtn = container
      .querySelectorAll('thead > tr > th')[1]
      .querySelector('.col-filter-btn') as HTMLElement;
    fireEvent.click(phaseBtn);
    expect(document.querySelector('.col-filter-popover')).not.toBeNull();
    unmount();
    expect(document.querySelector('.col-filter-popover')).toBeNull();
  });
});
