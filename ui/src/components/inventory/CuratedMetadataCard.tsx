import { FormEvent, useState } from 'react';
import type { ReactNode } from 'react';
import { canEdit, useMe } from '../../me';
import { KV, Labels } from '../../components';
import { formatKV, parseKV } from '../../kv';
import { Pill } from '../lv/Pill';

// CuratedMetadataCard is the inline read-then-edit pattern that the
// Cluster / Node / Namespace / VirtualMachine detail pages all need.
// It owns the read-vs-edit state, runs the patch through the caller-
// supplied saver, and bubbles reload to the parent on success.
//
// The shape is intentionally narrow: every entity in v1 carries
// owner / criticality / notes / runbook_url / annotations. Extras
// (display_name, role on a VM; hardware_model on a Node) are wired
// via the optional `extraFields` slot — the card renders them in the
// form and includes them in the saver payload.

export interface CuratedValues {
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
}

export interface CuratedExtraField {
  key: string;
  label: string;
  placeholder?: string;
  // Multiline = textarea instead of single-line input.
  multiline?: boolean;
  // Initial value coming from the entity row.
  initial: string;
}

// CuratedSaver receives the curated values plus any extra fields the
// page wired in. The page is responsible for translating them into the
// merge-patch the API expects and calling the right update function.
export type CuratedSaver = (
  values: CuratedValues,
  extras: Record<string, string>,
) => Promise<void>;

export function CuratedMetadataCard({
  values,
  extraDisplay,
  extraFields,
  onSave,
  onSaved,
  title = 'Ownership & context',
  emptyHint = 'No curated metadata yet. Use Edit to record owner, criticality, and a runbook link.',
}: {
  values: CuratedValues;
  // Extra rows to render in the read view (e.g. display_name, role).
  extraDisplay?: { label: string; value: ReactNode }[];
  // Extra inputs to render in the form.
  extraFields?: CuratedExtraField[];
  onSave: CuratedSaver;
  onSaved: () => void;
  title?: string;
  emptyHint?: string;
}) {
  const me = useMe();
  const [editing, setEditing] = useState(false);

  if (editing && canEdit(me)) {
    return (
      <CuratedForm
        values={values}
        extraFields={extraFields ?? []}
        onSave={onSave}
        onCancel={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved();
        }}
        title={title}
      />
    );
  }

  const empty =
    !values.owner &&
    !values.criticality &&
    !values.notes &&
    !values.runbook_url &&
    !values.annotations &&
    !(extraDisplay ?? []).some((d) => d.value);

  return (
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">{title}</h3>
        {canEdit(me) && (
          <button type="button" className="lv-btn lv-btn-primary" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {empty ? (
        <p className="muted" style={{ marginTop: 0 }}>
          {canEdit(me) ? emptyHint : 'No curated metadata recorded.'}
        </p>
      ) : (
        <dl className="kv-list">
          {extraDisplay?.map((d) => (
            <KV key={d.label} k={d.label} v={d.value} />
          ))}
          <KV k="Owner" v={values.owner} />
          <KV
            k="Criticality"
            v={values.criticality ? <Pill status="accent">{values.criticality}</Pill> : undefined}
          />
          <KV
            k="Runbook"
            v={
              values.runbook_url ? (
                <a href={values.runbook_url} target="_blank" rel="noreferrer">
                  {values.runbook_url}
                </a>
              ) : undefined
            }
          />
          <KV
            k="Notes"
            v={values.notes ? <pre className="curated-notes">{values.notes}</pre> : undefined}
          />
          <KV k="Annotations" v={<Labels labels={values.annotations} />} />
        </dl>
      )}
    </div>
  );
}

function CuratedForm({
  values,
  extraFields,
  onSave,
  onCancel,
  onSaved,
  title,
}: {
  values: CuratedValues;
  extraFields: CuratedExtraField[];
  onSave: CuratedSaver;
  onCancel: () => void;
  onSaved: () => void;
  title: string;
}) {
  const [owner, setOwner] = useState(values.owner ?? '');
  const [criticality, setCriticality] = useState(values.criticality ?? '');
  const [notes, setNotes] = useState(values.notes ?? '');
  const [runbook, setRunbook] = useState(values.runbook_url ?? '');
  const [annotationsText, setAnnotationsText] = useState(formatKV(values.annotations));
  const [extras, setExtras] = useState<Record<string, string>>(() =>
    Object.fromEntries(extraFields.map((f) => [f.key, f.initial])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    let annotations: Record<string, string>;
    try {
      annotations = parseKV(annotationsText, 'annotations');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return;
    }
    setBusy(true);
    try {
      await onSave(
        {
          owner: owner.trim(),
          criticality: criticality.trim(),
          notes: notes,
          runbook_url: runbook.trim(),
          annotations: annotations,
        },
        Object.fromEntries(
          Object.entries(extras).map(([k, v]) => [k, typeof v === 'string' ? v.trim() : v]),
        ),
      );
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Split inline (single-line) and multiline (textarea) extras so the
  // single-line ones share a row with owner / criticality where they fit.
  const inlineExtras = extraFields.filter((f) => !f.multiline);
  const multilineExtras = extraFields.filter((f) => f.multiline);

  return (
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">Edit {title.toLowerCase()}</h3>
      </div>
      <form className="admin-form" onSubmit={onSubmit}>
        <div className="admin-form-row">
          <div>
            <label>Owner</label>
            <input
              type="text"
              value={owner}
              onChange={(e) => setOwner(e.target.value)}
              placeholder="team-platform / oncall@example.com"
            />
          </div>
          <div>
            <label>Criticality</label>
            <input
              type="text"
              value={criticality}
              onChange={(e) => setCriticality(e.target.value)}
              placeholder="critical / high / medium / low"
            />
          </div>
          {inlineExtras.map((f) => (
            <div key={f.key}>
              <label>{f.label}</label>
              <input
                type="text"
                value={extras[f.key] ?? ''}
                onChange={(e) => setExtras((prev) => ({ ...prev, [f.key]: e.target.value }))}
                placeholder={f.placeholder}
              />
            </div>
          ))}
        </div>
        <div>
          <label>Runbook URL</label>
          <input
            type="url"
            value={runbook}
            onChange={(e) => setRunbook(e.target.value)}
            placeholder="https://runbooks.example.com/asset-name"
          />
        </div>
        <div style={{ marginTop: '0.75rem' }}>
          <label>Notes</label>
          <textarea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            rows={4}
            style={{ width: '100%' }}
          />
        </div>
        {multilineExtras.map((f) => (
          <div key={f.key} style={{ marginTop: '0.75rem' }}>
            <label>{f.label}</label>
            <textarea
              value={extras[f.key] ?? ''}
              onChange={(e) => setExtras((prev) => ({ ...prev, [f.key]: e.target.value }))}
              rows={3}
              style={{ width: '100%' }}
              placeholder={f.placeholder}
            />
          </div>
        ))}
        <div style={{ marginTop: '0.75rem' }}>
          <label>Annotations (one key=value per line)</label>
          <textarea
            value={annotationsText}
            onChange={(e) => setAnnotationsText(e.target.value)}
            rows={3}
            style={{
              width: '100%',
              fontFamily: 'var(--font-mono)',
            }}
          />
        </div>
        {error && <div className="error">{error}</div>}
        <div className="admin-form-actions">
          <button type="submit" className="lv-btn lv-btn-primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="lv-btn lv-btn-ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
