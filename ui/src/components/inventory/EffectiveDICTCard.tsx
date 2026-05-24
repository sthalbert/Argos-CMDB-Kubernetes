import { Link } from 'react-router-dom';
import type { EffectiveDICT } from '../../api';

// EffectiveDICTCard renders the read-only inherited DICT classification
// (ADR-0029 §6) on workload + VM detail pages. DICT lives only on
// applications — workloads/namespaces never got their own DICT columns —
// so this card never offers edit controls. Classification is set on the
// linked application's detail page (ApplicationDetail's DictForm).
//
// The card has two states:
//  - unclassified (dict null/undefined or source === 'none'): a muted hint
//    that points the operator at where to classify — the linked application
//    if there is one, or "link an application first" if not.
//  - classified (source === 'application'): the four EBIOS-RM axes as
//    heat-coloured cells plus a "Inherited from application X" source line.

interface Props {
  dict?: EffectiveDICT | null;
  linkedAppId?: string | null;
  linkedAppName?: string | null;
}

const AXES: Array<{ key: keyof EffectiveDICT; label: string; abbr: string }> = [
  { key: 'disponibilite', label: 'Disponibilité', abbr: 'D' },
  { key: 'integrite', label: 'Intégrité', abbr: 'I' },
  { key: 'confidentialite', label: 'Confidentialité', abbr: 'C' },
  { key: 'tracabilite', label: 'Traçabilité', abbr: 'T' },
];

export function EffectiveDICTCard({ dict, linkedAppId, linkedAppName }: Props) {
  const classified = dict != null && dict.source !== 'none';

  return (
    <section className="curated-card" data-testid="effective-dict-card">
      <div className="curated-card-header">
        <h3>Classification (DICT)</h3>
      </div>
      {!classified ? (
        <p className="muted">
          Not classified.{' '}
          {linkedAppName && linkedAppId ? (
            <>
              Set the security classification on application{' '}
              <Link to={`/applications/${linkedAppId}`}>{linkedAppName}</Link>.
            </>
          ) : (
            <>Link this resource to an application, then classify it.</>
          )}
        </p>
      ) : (
        <>
          <div className="dict-grid">
            {AXES.map((ax) => {
              const v = dict![ax.key] as number | null | undefined;
              return (
                <div key={ax.abbr} className="dict-cell">
                  <span className="dict-axis" title={ax.label}>
                    {ax.abbr}
                  </span>
                  <span
                    className={
                      typeof v === 'number' ? `dict-value heat-${v}` : 'dict-value'
                    }
                  >
                    {typeof v === 'number' ? v : '—'}
                  </span>
                  <span className="muted dict-label">{ax.label}</span>
                </div>
              );
            })}
          </div>
          {dict!.notes ? (
            <pre className="curated-notes" style={{ marginTop: '0.75rem' }}>
              {dict!.notes}
            </pre>
          ) : null}
          {linkedAppId && linkedAppName ? (
            <p className="muted" style={{ marginTop: '0.5rem' }}>
              Inherited from application{' '}
              <Link to={`/applications/${linkedAppId}`}>{linkedAppName}</Link>.
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}

export default EffectiveDICTCard;
