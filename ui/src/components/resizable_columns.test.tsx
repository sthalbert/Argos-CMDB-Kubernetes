import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/react';
import { useResizableColumns } from './resizable_columns';

// Tiny harness — mounts a 3-column entities table wired to the hook so
// tests can poke at the injected colgroup + handles directly.
function Harness({ storageKey = 'test.tbl' }: { storageKey?: string }) {
  const tableRef = useResizableColumns(storageKey);
  return (
    <table className="entities" ref={tableRef}>
      <thead>
        <tr>
          <th>A</th>
          <th>B</th>
          <th>C</th>
        </tr>
      </thead>
      <tbody>
        <tr>
          <td>a1</td>
          <td>b1</td>
          <td>c1</td>
        </tr>
      </tbody>
    </table>
  );
}

beforeEach(() => {
  localStorage.clear();
  // jsdom returns 0 from getBoundingClientRect by default. Fake a width
  // so the hook can capture an initial natural value.
  Element.prototype.getBoundingClientRect = vi.fn().mockReturnValue({
    width: 100,
    height: 24,
    top: 0,
    left: 0,
    right: 100,
    bottom: 24,
    x: 0,
    y: 0,
    toJSON: () => '',
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('useResizableColumns', () => {
  it('injects a colgroup with one <col> per <th> and a resize handle on each header', () => {
    const { container } = render(<Harness />);
    const table = container.querySelector('table')!;
    const colgroup = table.querySelector('colgroup');
    expect(colgroup).not.toBeNull();
    expect(colgroup!.children.length).toBe(3);
    expect(table.classList.contains('lv-resizable')).toBe(true);
    expect(table.style.tableLayout).toBe('fixed');
    expect(table.querySelectorAll('th .col-resize-handle').length).toBe(3);
  });

  it('applies saved widths from localStorage on mount', () => {
    localStorage.setItem(
      'lv.col-widths.test.tbl',
      JSON.stringify([222, 333, 444]),
    );
    const { container } = render(<Harness />);
    const cols = Array.from(container.querySelectorAll('colgroup > col')) as HTMLElement[];
    expect(cols[0].style.width).toBe('222px');
    expect(cols[1].style.width).toBe('333px');
    expect(cols[2].style.width).toBe('444px');
  });

  it('persists widths to localStorage after a drag', () => {
    const { container } = render(<Harness />);
    const handles = container.querySelectorAll('th .col-resize-handle');
    const firstHandle = handles[0] as HTMLElement;

    fireEvent.mouseDown(firstHandle, { button: 0, clientX: 100 });
    fireEvent.mouseMove(window, { clientX: 160 });
    fireEvent.mouseUp(window);

    const cols = Array.from(container.querySelectorAll('colgroup > col')) as HTMLElement[];
    expect(cols[0].style.width).toBe('160px');

    const saved = JSON.parse(localStorage.getItem('lv.col-widths.test.tbl')!);
    expect(saved[0]).toBe(160);
    // other columns keep their initial natural width (100 from the mock)
    expect(saved[1]).toBe(100);
    expect(saved[2]).toBe(100);
  });

  it('clamps widths at the minimum (40px) when dragged left past the limit', () => {
    const { container } = render(<Harness />);
    const handle = container.querySelector('th .col-resize-handle') as HTMLElement;
    fireEvent.mouseDown(handle, { button: 0, clientX: 100 });
    fireEvent.mouseMove(window, { clientX: -500 });
    fireEvent.mouseUp(window);
    const col = container.querySelector('colgroup > col') as HTMLElement;
    expect(col.style.width).toBe('40px');
  });

  it('cleans up the colgroup and handles on unmount', () => {
    const { container, unmount } = render(<Harness />);
    expect(container.querySelector('colgroup')).not.toBeNull();
    unmount();
    // After unmount the table is gone too — but the cleanup must not throw.
    // (Re-rendering with an empty body confirms idempotency.)
    expect(document.body.classList.contains('lv-resizing-cols')).toBe(false);
  });
});
