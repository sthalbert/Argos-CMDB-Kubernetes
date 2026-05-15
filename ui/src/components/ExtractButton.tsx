import { useEffect, useRef, useState } from 'react';
import type { ExtractFormat } from '../api';

export interface ExtractButtonProps {
  label: string;
  disabled?: boolean;
  onExtract: (format: ExtractFormat) => Promise<{ truncated: boolean; filename: string | null }>;
  /** Called with the truncation flag from the server. Optional. */
  onTruncation?: (truncated: boolean) => void;
}

/**
 * Small dropdown button: clicking opens a menu of [CSV, JSON]; clicking
 * an item invokes onExtract(format). Used on Search and EOL Dashboard.
 */
export function ExtractButton({ label, disabled, onExtract, onTruncation }: ExtractButtonProps) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (!wrapperRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, [open]);

  const choose = async (format: ExtractFormat) => {
    setOpen(false);
    setBusy(true);
    try {
      const result = await onExtract(format);
      if (onTruncation) onTruncation(result.truncated);
    } catch (e) {
      console.error('extract failed', e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div ref={wrapperRef} className="extract-button">
      <button
        type="button"
        disabled={disabled || busy}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        {busy ? `${label}…` : label} ▾
      </button>
      {open && (
        <ul role="menu" className="extract-menu">
          <li role="menuitem" onClick={() => choose('csv')}>CSV</li>
          <li role="menuitem" onClick={() => choose('json')}>JSON</li>
        </ul>
      )}
    </div>
  );
}
