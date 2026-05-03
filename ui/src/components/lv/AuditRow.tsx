import type { ReactNode } from 'react';

// AuditHeader renders a role="row" header with one role="columnheader" per
// column. Mount it once before the AuditRow loop so assistive technology can
// map cells to column labels (WCAG 1.3.1).
export function AuditHeader() {
  return (
    <div className="lv-audit-row lv-audit-header" role="row">
      <div className="lv-audit-time" role="columnheader">Time</div>
      <div className="lv-audit-actor" role="columnheader">Actor</div>
      <div className="lv-audit-msg" role="columnheader">Message</div>
      <div className="lv-audit-result" role="columnheader">HTTP</div>
      <div className="lv-audit-meta" role="columnheader">Source IP</div>
    </div>
  );
}

// AuditRow renders one audit event as a role="row" with role="cell" on each
// slot. The optional `meta` slot (column 5) is intended for Source IP so that
// incident investigators can trace the originating address without leaving the
// table view. Render it after `result` to keep the most-referenced columns
// (time, actor, message) on the left.
export function AuditRow({
  time,
  actor,
  message,
  result,
  meta,
  className,
}: {
  time: string;
  actor: ReactNode;
  message: ReactNode;
  result: ReactNode;
  /** Optional fifth column, used for Source IP. */
  meta?: ReactNode;
  className?: string;
}) {
  return (
    <div className={['lv-audit-row', className].filter(Boolean).join(' ')} role="row">
      <div className="lv-audit-time" role="cell">{time}</div>
      <div className="lv-audit-actor" role="cell">{actor}</div>
      <div className="lv-audit-msg" role="cell">{message}</div>
      <div className="lv-audit-result" role="cell">{result}</div>
      {meta !== undefined && <div className="lv-audit-meta" role="cell">{meta}</div>}
    </div>
  );
}
