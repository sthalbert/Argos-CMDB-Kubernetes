import type { ReactNode } from 'react';

export function AuditRow({
  time,
  actor,
  message,
  result,
  className,
}: {
  time: string;
  actor: string;
  message: ReactNode;
  result: ReactNode;
  className?: string;
}) {
  return (
    <div className={['lv-audit-row', className].filter(Boolean).join(' ')}>
      <div className="lv-audit-time">{time}</div>
      <div className="lv-audit-actor">{actor}</div>
      <div className="lv-audit-msg">{message}</div>
      <div className="lv-audit-result">{result}</div>
    </div>
  );
}
