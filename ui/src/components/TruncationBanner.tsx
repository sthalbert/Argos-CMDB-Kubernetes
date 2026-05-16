import { useState } from 'react';

export interface TruncationBannerProps {
  visible: boolean;
  rowCap?: number;
}

/**
 * Small dismissible warning shown after an extract returned
 * X-Longue-Vue-Truncated: true. The dismissed state is component-local.
 */
export function TruncationBanner({ visible, rowCap = 50000 }: TruncationBannerProps) {
  const [dismissed, setDismissed] = useState(false);
  if (!visible || dismissed) return null;
  return (
    <div className="banner banner-warn">
      <span>
        Last extract was capped at {rowCap.toLocaleString()} rows; narrow the filter for a complete view.
      </span>
      <button type="button" onClick={() => setDismissed(true)} aria-label="Dismiss">
        ✕
      </button>
    </div>
  );
}
