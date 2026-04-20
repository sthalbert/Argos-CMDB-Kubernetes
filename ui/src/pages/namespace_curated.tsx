import { FormEvent, useState } from 'react';
import * as api from '../api';
import { canEdit, useMe } from '../me';
import { KV, Labels } from '../components';
import { formatKV, parseKV } from '../kv';

// NamespaceCuratedCard mirrors ClusterCuratedCard but only surfaces the
// operator-owned fields relevant at the namespace level (owner,
// criticality, notes, runbook, annotations). Environment / region /
// provider live on the parent Cluster; DICT security-need columns will
// land in a later migration (00023) and get their own card.

export function NamespaceCuratedCard({
  namespace,
  onSaved,
}: {
  namespace: api.Namespace;
  onSaved: () => void;
}) {
  const me = useMe();
  const [editing, setEditing] = useState(false);
  if (editing && canEdit(me)) {
    return (
      <NamespaceCuratedForm
        namespace={namespace}
        onCancel={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved();
        }}
      />
    );
  }
  const empty =
    !namespace.owner &&
    !namespace.criticality &&
    !namespace.notes &&
    !namespace.runbook_url &&
    !namespace.annotations;
  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Ownership &amp; context</h3>
        {canEdit(me) && (
          <button type="button" className="primary" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {empty ? (
        <p className="muted" style={{ marginTop: 0 }}>
          {canEdit(me)
            ? 'No curated metadata yet. Use Edit to record owner, criticality, and a runbook link.'
            : 'No curated metadata recorded.'}
        </p>
      ) : (
        <dl className="kv-list">
          <KV k="Owner" v={namespace.owner} />
          <KV
            k="Criticality"
            v={
              namespace.criticality ? (
                <span className="pill">{namespace.criticality}</span>
              ) : undefined
            }
          />
          <KV
            k="Runbook"
            v={
              namespace.runbook_url ? (
                <a href={namespace.runbook_url} target="_blank" rel="noreferrer">
                  {namespace.runbook_url}
                </a>
              ) : undefined
            }
          />
          <KV
            k="Notes"
            v={
              namespace.notes ? (
                <pre className="curated-notes">{namespace.notes}</pre>
              ) : undefined
            }
          />
          <KV k="Annotations" v={<Labels labels={namespace.annotations} />} />
        </dl>
      )}
    </section>
  );
}

function NamespaceCuratedForm({
  namespace,
  onCancel,
  onSaved,
}: {
  namespace: api.Namespace;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [owner, setOwner] = useState(namespace.owner || '');
  const [criticality, setCriticality] = useState(namespace.criticality || '');
  const [notes, setNotes] = useState(namespace.notes || '');
  const [runbook, setRunbook] = useState(namespace.runbook_url || '');
  const [annotationsText, setAnnotationsText] = useState(formatKV(namespace.annotations));
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
      await api.updateNamespace(namespace.id, {
        owner: owner.trim(),
        criticality: criticality.trim(),
        notes: notes,
        runbook_url: runbook.trim(),
        annotations: annotations,
      });
      onSaved();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Edit ownership &amp; context</h3>
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
        </div>
        <div>
          <label>Runbook URL</label>
          <input
            type="url"
            value={runbook}
            onChange={(e) => setRunbook(e.target.value)}
            placeholder="https://runbooks.example.com/ns-prod"
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
        <div style={{ marginTop: '0.75rem' }}>
          <label>Annotations (one key=value per line)</label>
          <textarea
            value={annotationsText}
            onChange={(e) => setAnnotationsText(e.target.value)}
            rows={3}
            style={{
              width: '100%',
              fontFamily: 'ui-monospace, SFMono-Regular, Consolas, monospace',
            }}
          />
        </div>
        {error && <div className="error">{error}</div>}
        <div className="admin-form-actions">
          <button type="submit" className="primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="danger" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}
