// ClusterHistory — History tab for the cluster detail page (ADR-0021 Phase 4).
// Horizontal time-travel: bucketed bar track with two A/B handles bracketing a
// range; stats and diff list are filtered to events within [A, B].

import { useEffect, useMemo, useRef, useState } from 'react';
import * as api from '../api';

type ChangeType = api.HistoryRow['change_type'];

const CHANGE_LABEL: Record<ChangeType, string> = {
  create:      '+ created',
  update:      '~ changed',
  soft_delete: '− removed',
  restore:     '↺ restored',
};

const CHANGE_KIND: Record<ChangeType, 'add' | 'mod' | 'rem' | 'rst'> = {
  create:      'add',
  update:      'mod',
  soft_delete: 'rem',
  restore:     'rst',
};

const BUCKETS = 32;

function formatStamp(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}Z`;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

function formatValue(v: unknown): string {
  if (v === undefined || v === null) return '∅';
  if (typeof v === 'string')  return v;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  return JSON.stringify(v);
}

function shortActor(row: api.HistoryRow): string {
  const kind = row.actor_kind ?? 'system';
  const id = row.actor_id ? row.actor_id.slice(0, 8) : '—';
  return `${kind} · ${id}`;
}

interface Bucket {
  count: number;
  predominant: ChangeType | null;
  t: number; // bucket center timestamp (ms)
}

function bucketize(rows: api.HistoryRow[]): { buckets: Bucket[]; tMin: number; tMax: number } {
  if (rows.length === 0) {
    const now = Date.now();
    return { buckets: Array(BUCKETS).fill(null).map(() => ({ count: 0, predominant: null, t: now })), tMin: now, tMax: now };
  }
  const times = rows.map((r) => new Date(r.valid_from).getTime());
  let tMin = Math.min(...times);
  let tMax = Math.max(...times);
  if (tMin === tMax) {
    tMin -= 60_000;
    tMax += 60_000;
  }
  const span = tMax - tMin;
  const counts: Record<ChangeType, number>[] = Array(BUCKETS).fill(null).map(() => ({ create: 0, update: 0, soft_delete: 0, restore: 0 }));
  for (const r of rows) {
    const t = new Date(r.valid_from).getTime();
    const idx = Math.min(BUCKETS - 1, Math.max(0, Math.floor(((t - tMin) / span) * BUCKETS)));
    counts[idx][r.change_type]++;
  }
  const buckets: Bucket[] = counts.map((c, i) => {
    const total = c.create + c.update + c.soft_delete + c.restore;
    let predominant: ChangeType | null = null;
    let max = 0;
    (Object.keys(c) as ChangeType[]).forEach((k) => {
      if (c[k] > max) { max = c[k]; predominant = k; }
    });
    return { count: total, predominant, t: tMin + (span * (i + 0.5)) / BUCKETS };
  });
  return { buckets, tMin, tMax };
}

export function ClusterHistory({ clusterId }: { clusterId: string }) {
  const [rows, setRows] = useState<api.HistoryRow[] | null>(null);
  const [nextCursor, setNextCursor] = useState('');
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [aPos, setAPos] = useState(0);
  const [bPos, setBPos] = useState(BUCKETS - 1);
  const trackRef = useRef<HTMLDivElement | null>(null);
  const draggingRef = useRef<'a' | 'b' | null>(null);

  const bucketFromClientX = (clientX: number): number | null => {
    if (!trackRef.current) return null;
    const r = trackRef.current.getBoundingClientRect();
    const pct = (clientX - r.left) / r.width;
    return Math.max(0, Math.min(BUCKETS - 1, Math.round(pct * (BUCKETS - 1))));
  };

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return;
      const b = bucketFromClientX(e.clientX);
      if (b === null) return;
      if (draggingRef.current === 'a') setAPos(b);
      else setBPos(b);
    };
    const onUp = () => { draggingRef.current = null; };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    api
      .listEntityHistory('clusters', clusterId)
      .then((page) => {
        if (cancelled) return;
        setRows(page.items ?? []);
        setNextCursor(page.next_cursor ?? '');
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof api.ApiError && err.status === 503) {
          setError('History is disabled in settings — toggle it in Admin → Settings.');
        } else {
          setError('Failed to load history.');
        }
        setRows([]);
      });
    return () => { cancelled = true; };
  }, [clusterId]);

  const handleLoadMore = async () => {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await api.listEntityHistory('clusters', clusterId, nextCursor);
      setRows((prev) => [...(prev ?? []), ...(page.items ?? [])]);
      setNextCursor(page.next_cursor ?? '');
    } catch {
      /* swallow */
    } finally {
      setLoadingMore(false);
    }
  };

  const meaningful = useMemo(() => (rows ?? []).filter((r) => r.diff && r.diff.length > 0), [rows]);
  const { buckets, tMin, tMax } = useMemo(() => bucketize(meaningful), [meaningful]);
  const maxCount = useMemo(() => Math.max(1, ...buckets.map((b) => b.count)), [buckets]);

  const aBucket = Math.min(aPos, bPos);
  const bBucket = Math.max(aPos, bPos);
  const span = tMax - tMin || 1;
  const aT = aBucket === 0 ? tMin : tMin + (span * aBucket) / BUCKETS;
  const bT = bBucket === BUCKETS - 1 ? tMax : tMin + (span * (bBucket + 1)) / BUCKETS;

  const inRange = useMemo(() => {
    if (!rows) return [];
    return rows.filter((r) => {
      if (!r.diff || r.diff.length === 0) return false;
      const t = new Date(r.valid_from).getTime();
      return t >= aT && t <= bT;
    });
  }, [rows, aT, bT]);

  const stats = useMemo(() => {
    const s = { add: 0, mod: 0, rem: 0 };
    for (const r of inRange) {
      if (r.change_type === 'create' || r.change_type === 'restore') s.add++;
      else if (r.change_type === 'update')                          s.mod++;
      else if (r.change_type === 'soft_delete')                     s.rem++;
    }
    return s;
  }, [inRange]);

  const handleTrackClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (draggingRef.current) return;
    const bucket = bucketFromClientX(e.clientX);
    if (bucket === null) return;
    if (Math.abs(bucket - aPos) <= Math.abs(bucket - bPos)) setAPos(bucket);
    else setBPos(bucket);
  };

  const startDrag = (which: 'a' | 'b') => (e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    draggingRef.current = which;
  };

  if (rows === null) {
    return <div className="lv-tt-empty"><span className="glyph">· · ·</span>loading history</div>;
  }
  if (error) {
    return (
      <div className="lv-tt-empty" style={{ color: 'var(--bad-fg)' }}>
        <span className="glyph">⌀</span>{error}
      </div>
    );
  }
  if (rows.length === 0) {
    return <div className="lv-tt-empty"><span className="glyph">∅</span>no history recorded yet</div>;
  }

  const aLeft = (aPos / (BUCKETS - 1)) * 100;
  const bLeft = (bPos / (BUCKETS - 1)) * 100;
  const rangeLeft = Math.min(aLeft, bLeft);
  const rangeWidth = Math.abs(bLeft - aLeft);

  return (
    <div className="lv-tt-card">
      <div className="lv-tt-head">
        <div className="lv-tt-title">{formatDate(new Date(tMin).toISOString())} — {formatDate(new Date(tMax).toISOString())}</div>
        <div className="lv-tt-points">
          <span className="lv-tt-pt"><span className="dot" />A · {formatStamp(new Date(aT).toISOString())}</span>
          <span className="lv-tt-arrow">→</span>
          <span className="lv-tt-pt b"><span className="dot" />B · {formatStamp(new Date(bT).toISOString())}</span>
        </div>
      </div>

      <div className="lv-tt-track" ref={trackRef} onClick={handleTrackClick}>
        <div className="lv-tt-bars">
          {buckets.map((b, i) => {
            const inR = i >= aBucket && i <= bBucket;
            const h = b.count === 0 ? 4 : 18 + Math.round((b.count / maxCount) * 30);
            const kind = b.count === 0 ? 'empty' : `k-${CHANGE_KIND[b.predominant!]}`;
            return (
              <div
                key={i}
                className={`lv-tt-bar ${kind} ${inR ? 'in-range' : 'out-range'}`}
                style={{ height: `${h}px` }}
              />
            );
          })}
        </div>
        {meaningful.map((r) => {
          const t = new Date(r.valid_from).getTime();
          const span2 = (tMax - tMin) || 1;
          const left = ((t - tMin) / span2) * 100;
          const inR = t >= aT && t <= bT;
          return (
            <div
              key={r.history_id}
              className={`lv-tt-tick ${CHANGE_KIND[r.change_type]} ${inR ? 'in-range' : 'out-range'}`}
              style={{ left: `${left}%` }}
              title={`${CHANGE_LABEL[r.change_type]} · ${formatStamp(r.valid_from)}`}
            />
          );
        })}
        <div className="lv-tt-range" style={{ left: `${rangeLeft}%`, width: `${rangeWidth}%` }} />
        <div className="lv-tt-handle" style={{ left: `${aLeft}%` }} data-label="A" onMouseDown={startDrag('a')} />
        <div className="lv-tt-handle b" style={{ left: `${bLeft}%` }} data-label="B" onMouseDown={startDrag('b')} />
      </div>
      <div className="lv-tt-axis">
        <span>{formatDate(new Date(tMin).toISOString())}</span>
        <span className="hint">click to move the nearest handle</span>
        <span>{formatDate(new Date(tMax).toISOString())}</span>
      </div>

      <div className="lv-tt-stats">
        <div className="lv-tt-stat">
          <div className="lv-tt-stat-label">Window</div>
          <div className="lv-tt-stat-value accent">{inRange.length}</div>
          <div className="lv-tt-stat-meta">events between A and B</div>
        </div>
        <div className="lv-tt-stat">
          <div className="lv-tt-stat-label">Added</div>
          <div className="lv-tt-stat-value ok">+{stats.add}</div>
          <div className="lv-tt-stat-meta">creates &amp; restores</div>
        </div>
        <div className="lv-tt-stat">
          <div className="lv-tt-stat-label">Modified</div>
          <div className="lv-tt-stat-value warn">~{stats.mod}</div>
          <div className="lv-tt-stat-meta">field-level changes</div>
        </div>
        <div className="lv-tt-stat">
          <div className="lv-tt-stat-label">Removed</div>
          <div className="lv-tt-stat-value bad">−{stats.rem}</div>
          <div className="lv-tt-stat-meta">soft-deletes</div>
        </div>
      </div>

      <div className="lv-tt-section">
        <span className="h">events in window</span>
        <span className="c">{inRange.length}</span>
      </div>

      <div className="lv-tt-list">
        {inRange.length === 0 && (
          <div className="lv-tt-list-empty"><span className="glyph">∅</span> no events in this window — widen the range</div>
        )}
        {inRange.length > 0 && inRange.map((row) => {
            const kind = CHANGE_KIND[row.change_type];
            return (
              <div key={row.history_id} className={`lv-tt-row ${kind}`}>
                <div className="lv-tt-tag">
                  <span>{CHANGE_LABEL[row.change_type]}</span>
                  <span className="lv-tt-tag-time">{formatStamp(row.valid_from)}</span>
                </div>
                <div>
                  <div className="lv-tt-target">cluster {row.entity_id.slice(0, 8)}</div>
                  <div className="lv-tt-detail">
                    <span className="actor">{shortActor(row)}</span>
                  </div>
                  {row.diff && row.diff.length > 0 && (
                    <div className="lv-tt-fields">
                      {row.diff.map((op, i) => (
                        <span key={i} className="lv-tt-fchip">
                          <span className="name">{op.path.replace(/^\//, '')}</span>
                          {op.from !== undefined && <span className="from">{formatValue(op.from)}</span>}
                          {op.from !== undefined && op.to !== undefined && <span className="arrow">→</span>}
                          {op.to !== undefined && <span className="to">{formatValue(op.to)}</span>}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            );
          })}
      </div>

      {nextCursor && (
        <div style={{ marginTop: '1rem', textAlign: 'center' }}>
          <button onClick={handleLoadMore} disabled={loadingMore}>
            {loadingMore ? 'loading…' : 'load older history'}
          </button>
        </div>
      )}
    </div>
  );
}
