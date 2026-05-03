import type { ReactNode } from 'react';

export type CalloutTone = 'ok' | 'warn' | 'bad';

export function Callout({
  title,
  status,
  className,
  children,
}: {
  title: ReactNode;
  status?: CalloutTone;
  className?: string;
  children?: ReactNode;
}) {
  const cls = ['lv-callout', status, className].filter(Boolean).join(' ');
  return (
    <div className={cls}>
      <strong>{title}</strong>
      {children !== undefined && children !== null && <> — {children}</>}
    </div>
  );
}
