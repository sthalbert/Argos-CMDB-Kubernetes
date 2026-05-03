import type { ReactNode } from 'react';

export function Section({ children, count, className }: { children: ReactNode; count?: number; className?: string }) {
  return (
    <h3 className={['section-title', className].filter(Boolean).join(' ')}>
      <span>{children}</span>
      {count !== undefined && <span className="count">· {count}</span>}
      <span className="section-rule" />
    </h3>
  );
}
