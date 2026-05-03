import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';

type TriggerProps = { open: boolean; toggle: () => void };
type BodyProps = { close: () => void };

export function Disclosure({
  trigger: Trigger,
  children,
  side = 'right',
}: {
  trigger: (p: TriggerProps) => ReactNode;
  children: ((p: BodyProps) => ReactNode) | ReactNode;
  side?: 'left' | 'right';
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLSpanElement | null>(null);
  const location = useLocation();
  const close = () => setOpen(false);
  const toggle = () => setOpen((o) => !o);

  useEffect(() => { setOpen(false); }, [location.pathname]);

  useEffect(() => {
    if (!open) return;
    const onDocDown = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDocDown);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <span className="lv-popover-relative" ref={wrapRef}>
      {Trigger({ open, toggle })}
      {open && (
        <div className="lv-popover" data-side={side}>
          {typeof children === 'function' ? (children as (p: BodyProps) => ReactNode)({ close }) : children}
        </div>
      )}
    </span>
  );
}
