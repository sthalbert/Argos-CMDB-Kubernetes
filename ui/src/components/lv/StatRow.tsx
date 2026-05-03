import type { ReactNode } from 'react';

export type StatTone = 'accent' | 'ok' | 'warn' | 'bad';

export function StatRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={['lv-stat-row', className].filter(Boolean).join(' ')}>{children}</div>;
}

export function Stat({
  label,
  value,
  tone,
  meta,
}: {
  label: string;
  value: ReactNode;
  tone?: StatTone;
  meta?: ReactNode;
}) {
  return (
    <div className="lv-stat">
      <div className="lv-stat-label">{label}</div>
      <div className={['lv-stat-value', tone].filter(Boolean).join(' ')}>{value}</div>
      {meta !== undefined && meta !== null && <div className="lv-stat-meta">{meta}</div>}
    </div>
  );
}
