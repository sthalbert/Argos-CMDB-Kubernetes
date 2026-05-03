import type { ReactNode } from 'react';

export function PageHead({
  title,
  sub,
  actions,
  className,
}: {
  title: ReactNode;
  sub?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <header className={['lv-page-head', className].filter(Boolean).join(' ')}>
      <div>
        <h1 className="lv-page-title">{title}</h1>
        {sub !== undefined && sub !== null && <div className="lv-page-sub">{sub}</div>}
      </div>
      {actions !== undefined && actions !== null && <div className="lv-page-actions">{actions}</div>}
    </header>
  );
}
