import { useCallback, useEffect, useRef } from 'react';

// useResizableColumns equips a <table> with draggable column-resize handles
// and a <colgroup> whose widths are persisted to localStorage.
//
// Usage:
//   const tableRef = useResizableColumns('lists.clusters');
//   <table className="entities" ref={tableRef}>
//     <thead><tr><th>…</th>…</tr></thead>
//     <tbody>…</tbody>
//   </table>
//
// On mount the hook measures each <th> for its natural width, switches the
// table to `table-layout: fixed`, and renders inline widths via an injected
// <colgroup>. Dragging the right edge of a header updates that column's
// width live; releasing persists the full row of widths under
// `lv.col-widths.<storageKey>`. Double-clicking a handle resets that column
// to its current natural content width and persists.
//
// The hook tolerates re-renders (it ignores subsequent ref calls for the
// same element) and cleans up handles + the injected colgroup on unmount.

const STORAGE_PREFIX = 'lv.col-widths.';
const MIN_WIDTH = 40;
// Cap on the initial natural-width capture so that a single long unbroken
// token in a tbody cell (think `ubuntu-22.04-amd64-very-long-image-name`)
// can't seed a 1000px column that blows the table past its container.
// Users can still drag a column wider — this only bounds the first paint.
const INITIAL_MAX_WIDTH = 320;

function loadWidths(key: string): number[] {
  try {
    const raw = localStorage.getItem(STORAGE_PREFIX + key);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.map((v) => Number(v)).filter((n) => Number.isFinite(n) && n > 0);
  } catch {
    return [];
  }
}

function saveWidths(key: string, widths: number[]) {
  try {
    localStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(widths));
  } catch {
    /* quota / disabled storage — ignored */
  }
}

export function useResizableColumns(storageKey: string) {
  const cleanupRef = useRef<(() => void) | null>(null);
  const equippedRef = useRef<HTMLTableElement | null>(null);

  // Tear down on unmount.
  useEffect(() => {
    return () => {
      if (cleanupRef.current) cleanupRef.current();
      cleanupRef.current = null;
      equippedRef.current = null;
    };
  }, []);

  return useCallback(
    (table: HTMLTableElement | null) => {
      // Already equipped this exact node — nothing to do (avoids React
      // re-render thrash). If a different node arrives, tear down first.
      if (equippedRef.current === table) return;
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }
      equippedRef.current = table;
      if (!table) return;

      const headRow = table.querySelector<HTMLTableRowElement>(
        ':scope > thead > tr',
      );
      if (!headRow) return;
      const ths = Array.from(
        headRow.querySelectorAll<HTMLTableCellElement>(':scope > th'),
      );
      if (ths.length === 0) return;

      const saved = loadWidths(storageKey);

      // Capture each th's natural width before we change layout, so we can
      // hand the table a sensible initial colgroup that matches its current
      // appearance.
      const widths: number[] = ths.map((th, i) => {
        if (saved[i] && saved[i] > 0) return saved[i];
        const natural = Math.round(th.getBoundingClientRect().width);
        return Math.min(INITIAL_MAX_WIDTH, Math.max(MIN_WIDTH, natural));
      });

      // Build (or reuse) a colgroup. We mark our own with data-lv-resize
      // so cleanup can remove only what we created.
      let colgroup = table.querySelector<HTMLTableColElement>(':scope > colgroup');
      const ownsColgroup = !colgroup;
      if (!colgroup) {
        colgroup = document.createElement('colgroup');
        colgroup.dataset.lvResize = '1';
        table.insertBefore(colgroup, table.firstChild);
      }
      const cols: HTMLTableColElement[] = ths.map((_, i) => {
        let col = colgroup!.children[i] as HTMLTableColElement | undefined;
        if (!col) {
          col = document.createElement('col');
          colgroup!.appendChild(col);
        }
        col.style.width = `${widths[i]}px`;
        return col;
      });

      const prevTableLayout = table.style.tableLayout;
      table.style.tableLayout = 'fixed';
      table.classList.add('lv-resizable');

      const cleanups: Array<() => void> = [];

      ths.forEach((th, i) => {
        const handle = document.createElement('div');
        handle.className = 'col-resize-handle';
        handle.setAttribute('aria-hidden', 'true');
        handle.title = 'Drag to resize · double-click to reset';
        th.appendChild(handle);

        const onMouseDown = (e: MouseEvent) => {
          if (e.button !== 0) return;
          e.preventDefault();
          e.stopPropagation();
          const startX = e.clientX;
          const startWidth = cols[i].getBoundingClientRect().width || widths[i];
          document.body.classList.add('lv-resizing-cols');

          const onMove = (ev: MouseEvent) => {
            const dx = ev.clientX - startX;
            const newW = Math.max(MIN_WIDTH, Math.round(startWidth + dx));
            widths[i] = newW;
            cols[i].style.width = `${newW}px`;
          };
          const onUp = () => {
            window.removeEventListener('mousemove', onMove);
            window.removeEventListener('mouseup', onUp);
            document.body.classList.remove('lv-resizing-cols');
            saveWidths(storageKey, widths);
          };
          window.addEventListener('mousemove', onMove);
          window.addEventListener('mouseup', onUp);
        };

        const onDblClick = (e: MouseEvent) => {
          e.preventDefault();
          e.stopPropagation();
          // Briefly drop the inline width so the browser remeasures, then
          // snap to the resulting natural width and persist.
          cols[i].style.width = '';
          const natural = Math.max(MIN_WIDTH, Math.round(th.getBoundingClientRect().width));
          widths[i] = natural;
          cols[i].style.width = `${natural}px`;
          saveWidths(storageKey, widths);
        };

        handle.addEventListener('mousedown', onMouseDown);
        handle.addEventListener('dblclick', onDblClick);
        cleanups.push(() => {
          handle.removeEventListener('mousedown', onMouseDown);
          handle.removeEventListener('dblclick', onDblClick);
          if (handle.parentNode) handle.parentNode.removeChild(handle);
        });
      });

      cleanupRef.current = () => {
        cleanups.forEach((c) => c());
        if (ownsColgroup && colgroup && colgroup.parentNode) {
          colgroup.parentNode.removeChild(colgroup);
        }
        table.style.tableLayout = prevTableLayout;
        table.classList.remove('lv-resizable');
        document.body.classList.remove('lv-resizing-cols');
      };
    },
    [storageKey],
  );
}
