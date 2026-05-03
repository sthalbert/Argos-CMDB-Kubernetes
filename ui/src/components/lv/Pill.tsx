import type { ReactNode } from 'react';

export type PillStatus = 'ok' | 'warn' | 'bad' | 'accent';

export function Pill({
  status,
  className,
  children,
}: {
  status?: PillStatus;
  className?: string;
  children: ReactNode;
}) {
  const cls = ['pill', status, className].filter(Boolean).join(' ');
  return <span className={cls}>{children}</span>;
}
