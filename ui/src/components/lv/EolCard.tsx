export type EolStatus = 'ok' | 'warn' | 'bad';

export function EolCard({
  status,
  count,
  label,
  meta,
  active,
  onClick,
  className,
}: {
  status: EolStatus;
  count: number;
  label: string;
  meta: string;
  active?: boolean;
  onClick?: () => void;
  className?: string;
}) {
  const cls = [
    'eol-summary-card',
    status,
    `eol-${status}`,
    active && 'active',
    className,
  ].filter(Boolean).join(' ');
  const body = (
    <>
      <div className="eol-count">{count}</div>
      <div className="eol-label">{label}</div>
      <div className="eol-meta">{meta}</div>
    </>
  );
  return onClick
    ? <button type="button" className={cls} onClick={onClick} aria-pressed={active}>{body}</button>
    : <div className={cls}>{body}</div>;
}
