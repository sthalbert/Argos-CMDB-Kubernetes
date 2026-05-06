import { useCallback, useEffect, useRef } from 'react';
import { useResizableColumns as useResizableColumnsImpl } from './resizable_columns';

// useColumnFilters equips a <table> with per-column "filter values" pickers.
//
// Usage:
//   const filterRef = useColumnFilters('lists.clusters');
//   <table className="entities" ref={filterRef}> … </table>
//
// On mount the hook walks the header row and appends a small funnel button
// to each <th> (skipping right-aligned action columns and any th carrying
// `data-no-filter`). Clicking the button opens a popover that lists the
// distinct trimmed text values currently rendered in that column's cells;
// each value is a checkbox. Toggling a value hides every row whose cell
// text in that column does not match the surviving set. Multiple columns
// AND together. Selections persist under `lv.col-filters.<storageKey>`.
//
// The hook re-applies its filter whenever React swaps rows under <tbody>
// (via a MutationObserver), so refetches and sort changes don't lose the
// active filter. Plays nicely alongside useResizableColumns — the funnel
// sits inline in the header, the resize handle hugs the right edge.

const STORAGE_PREFIX = 'lv.col-filters.';
const FILTER_ICON_SVG =
  '<svg viewBox="0 0 16 16" width="11" height="11" fill="currentColor" aria-hidden="true">' +
  '<path d="M1.5 2.5a.5.5 0 0 1 .5-.5h12a.5.5 0 0 1 .4.8L10 9.1V13a.5.5 0 0 1-.8.4l-2-1.4a.5.5 0 0 1-.2-.4V9.1L1.6 3.3A.5.5 0 0 1 1.5 2.5z"/>' +
  '</svg>';

type FilterMap = Record<string, string[]>;

function loadFilters(key: string): FilterMap {
  try {
    const raw = localStorage.getItem(STORAGE_PREFIX + key);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const out: FilterMap = {};
      for (const [k, v] of Object.entries(parsed)) {
        if (Array.isArray(v)) out[k] = v.map(String);
      }
      return out;
    }
    return {};
  } catch {
    return {};
  }
}

function saveFilters(key: string, filters: FilterMap) {
  try {
    if (Object.keys(filters).length === 0) {
      localStorage.removeItem(STORAGE_PREFIX + key);
    } else {
      localStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(filters));
    }
  } catch {
    /* quota / disabled storage — ignored */
  }
}

function cellText(cell: Element | null | undefined): string {
  return ((cell?.textContent) || '').trim().replace(/\s+/g, ' ');
}

function applyFilters(table: HTMLTableElement, filters: FilterMap) {
  const tbody = table.querySelector<HTMLTableSectionElement>(':scope > tbody');
  if (!tbody) return;
  const rows = Array.from(tbody.querySelectorAll<HTMLTableRowElement>(':scope > tr'));
  const activeIdx = Object.keys(filters)
    .map(Number)
    .filter((i) => Number.isInteger(i) && Array.isArray(filters[String(i)]));
  if (activeIdx.length === 0) {
    rows.forEach((r) => {
      r.style.display = '';
    });
    return;
  }
  rows.forEach((r) => {
    let show = true;
    for (const i of activeIdx) {
      const allowed = filters[String(i)];
      const text = cellText(r.children[i]);
      if (!allowed.includes(text)) {
        show = false;
        break;
      }
    }
    r.style.display = show ? '' : 'none';
  });
}

let openPopover: { el: HTMLDivElement; cleanup: () => void } | null = null;

function closePopover() {
  if (openPopover) {
    openPopover.cleanup();
    openPopover.el.remove();
    openPopover = null;
  }
}

function buildPopover(
  table: HTMLTableElement,
  colIdx: number,
  anchor: HTMLElement,
  filters: FilterMap,
  storageKey: string,
  onAfterChange: () => void,
) {
  // Distinct text values currently in this column.
  const tbody = table.querySelector<HTMLTableSectionElement>(':scope > tbody');
  const valueSet = new Set<string>();
  if (tbody) {
    tbody.querySelectorAll<HTMLTableRowElement>(':scope > tr').forEach((r) => {
      valueSet.add(cellText(r.children[colIdx]));
    });
  }
  const values = Array.from(valueSet).sort((a, b) => a.localeCompare(b));

  const stored = filters[String(colIdx)];
  // Working set tracks which values are currently kept. If no filter was
  // saved, treat that as "all kept" so toggling a checkbox reveals an
  // explicit selection (everything-but-this).
  const kept = new Set<string>(stored ?? values);

  const pop = document.createElement('div');
  pop.className = 'col-filter-popover';
  pop.setAttribute('role', 'dialog');
  pop.addEventListener('mousedown', (e) => e.stopPropagation());

  const head = document.createElement('div');
  head.className = 'col-filter-popover-head';
  const title = document.createElement('span');
  title.className = 'col-filter-popover-title';
  title.textContent = `Filter · ${values.length} value${values.length === 1 ? '' : 's'}`;
  const actions = document.createElement('div');
  actions.className = 'col-filter-popover-actions';
  const allBtn = document.createElement('button');
  allBtn.type = 'button';
  allBtn.className = 'link-btn';
  allBtn.textContent = 'All';
  const noneBtn = document.createElement('button');
  noneBtn.type = 'button';
  noneBtn.className = 'link-btn';
  noneBtn.textContent = 'None';
  actions.appendChild(allBtn);
  actions.appendChild(noneBtn);
  head.appendChild(title);
  head.appendChild(actions);
  pop.appendChild(head);

  const list = document.createElement('div');
  list.className = 'col-filter-popover-list';
  pop.appendChild(list);

  const persist = () => {
    if (kept.size === values.length) {
      delete filters[String(colIdx)];
    } else {
      filters[String(colIdx)] = Array.from(kept);
    }
    saveFilters(storageKey, filters);
    onAfterChange();
  };

  const renderRows = () => {
    list.replaceChildren();
    if (values.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'col-filter-popover-empty';
      empty.textContent = 'No values to filter.';
      list.appendChild(empty);
      return;
    }
    values.forEach((val) => {
      const row = document.createElement('label');
      row.className = 'col-filter-popover-item';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.checked = kept.has(val);
      cb.addEventListener('change', () => {
        if (cb.checked) kept.add(val);
        else kept.delete(val);
        persist();
      });
      const txt = document.createElement('span');
      txt.className = 'col-filter-popover-label';
      txt.textContent = val === '' ? '(empty)' : val;
      txt.title = val;
      row.appendChild(cb);
      row.appendChild(txt);
      list.appendChild(row);
    });
  };
  renderRows();

  allBtn.addEventListener('click', (e) => {
    e.preventDefault();
    kept.clear();
    values.forEach((v) => kept.add(v));
    persist();
    list
      .querySelectorAll<HTMLInputElement>('input[type=checkbox]')
      .forEach((cb) => {
        cb.checked = true;
      });
  });
  noneBtn.addEventListener('click', (e) => {
    e.preventDefault();
    kept.clear();
    persist();
    list
      .querySelectorAll<HTMLInputElement>('input[type=checkbox]')
      .forEach((cb) => {
        cb.checked = false;
      });
  });

  // Position via fixed coords next to the anchor. If the popover would
  // overflow the right edge, anchor it to the right side of the button
  // instead.
  document.body.appendChild(pop);
  const rect = anchor.getBoundingClientRect();
  const popWidth = pop.offsetWidth || 220;
  let left = rect.left;
  if (left + popWidth + 8 > window.innerWidth) {
    left = Math.max(8, window.innerWidth - popWidth - 8);
  }
  pop.style.position = 'fixed';
  pop.style.top = `${rect.bottom + 4}px`;
  pop.style.left = `${left}px`;

  const onDocDown = (e: MouseEvent) => {
    if (pop.contains(e.target as Node) || anchor.contains(e.target as Node)) return;
    closePopover();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') closePopover();
  };
  document.addEventListener('mousedown', onDocDown);
  document.addEventListener('keydown', onKey);

  openPopover = {
    el: pop,
    cleanup: () => {
      document.removeEventListener('mousedown', onDocDown);
      document.removeEventListener('keydown', onKey);
    },
  };
}

// useEntityTable bundles the two table enhancements (column resizing +
// per-column value filters) behind a single ref so a page only has to
// thread one ref onto its <table>. Both behaviors persist under the
// same storage key (different prefixes internally).
export function useEntityTable(storageKey: string) {
  // Imported lazily to keep the hook order trivial — both children obey
  // the rules-of-hooks because they're called unconditionally below.
  /* eslint-disable react-hooks/rules-of-hooks */
  const resizeRef = useResizableColumnsImpl(storageKey);
  const filterRef = useColumnFilters(storageKey);
  /* eslint-enable react-hooks/rules-of-hooks */
  return useCallback(
    (el: HTMLTableElement | null) => {
      resizeRef(el);
      filterRef(el);
    },
    [resizeRef, filterRef],
  );
}

export function useColumnFilters(storageKey: string) {
  const cleanupRef = useRef<(() => void) | null>(null);
  const equippedRef = useRef<HTMLTableElement | null>(null);

  useEffect(() => {
    return () => {
      if (cleanupRef.current) cleanupRef.current();
      cleanupRef.current = null;
      equippedRef.current = null;
      closePopover();
    };
  }, []);

  return useCallback(
    (table: HTMLTableElement | null) => {
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

      const filters = loadFilters(storageKey);
      const cleanups: Array<() => void> = [];
      const updaters: Array<() => void> = [];

      ths.forEach((th, idx) => {
        // Skip right-aligned (action) columns and explicit opt-outs.
        const skip =
          th.style.textAlign === 'right' ||
          th.hasAttribute('data-no-filter');
        if (skip) return;

        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'col-filter-btn';
        btn.innerHTML = FILTER_ICON_SVG;
        btn.setAttribute('aria-label', 'Filter values');
        btn.title = 'Filter values';
        th.appendChild(btn);

        const updateActive = () => {
          const has = Array.isArray(filters[String(idx)]);
          btn.classList.toggle('col-filter-active', has);
        };
        updateActive();
        updaters.push(updateActive);

        const onClick = (e: MouseEvent) => {
          e.preventDefault();
          e.stopPropagation();
          // Re-clicking the funnel while its popover is open closes it.
          if (openPopover && openPopover.el.dataset.colIdx === String(idx)) {
            closePopover();
            return;
          }
          closePopover();
          buildPopover(table, idx, btn, filters, storageKey, () => {
            applyFilters(table, filters);
            updaters.forEach((u) => u());
          });
          if (openPopover) openPopover.el.dataset.colIdx = String(idx);
        };
        btn.addEventListener('click', onClick);
        cleanups.push(() => {
          btn.removeEventListener('click', onClick);
          if (btn.parentNode) btn.parentNode.removeChild(btn);
        });
      });

      // Apply persisted filters straight away so reloads land on the same
      // visible subset the user left behind.
      applyFilters(table, filters);

      // Re-apply when React swaps rows (refetch, resort, paginate).
      const tbody = table.querySelector<HTMLTableSectionElement>(':scope > tbody');
      let observer: MutationObserver | null = null;
      if (tbody && typeof MutationObserver !== 'undefined') {
        observer = new MutationObserver(() => applyFilters(table, filters));
        observer.observe(tbody, { childList: true });
      }

      cleanupRef.current = () => {
        if (observer) observer.disconnect();
        cleanups.forEach((c) => c());
        // Restore visibility — our display:none is otherwise sticky.
        const rows = table.querySelectorAll<HTMLTableRowElement>(
          ':scope > tbody > tr',
        );
        rows.forEach((r) => {
          r.style.display = '';
        });
        closePopover();
      };
    },
    [storageKey],
  );
}
