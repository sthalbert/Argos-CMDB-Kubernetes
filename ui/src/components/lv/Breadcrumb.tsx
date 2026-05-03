import { Fragment } from 'react';
import { Link } from 'react-router-dom';

export type BreadcrumbPart = { label: string; to?: string; ariaLabel?: string };

export function Breadcrumb({ parts, className }: { parts: BreadcrumbPart[]; className?: string }) {
  return (
    <div className={['breadcrumb', className].filter(Boolean).join(' ')}>
      {parts.map((p, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="breadcrumb-sep">/</span>}
          {p.to ? <Link to={p.to} aria-label={p.ariaLabel}>{p.label}</Link> : <span>{p.label}</span>}
        </Fragment>
      ))}
    </div>
  );
}
