import { FormEvent, useState } from 'react';
import * as api from '../api';
import { canEdit, useMe } from '../me';
import { KV, Labels } from '../components';
import { Pill } from '../components/lv/Pill';
import { formatKV, parseKV } from '../kv';

// NodeCuratedCard surfaces the operator-owned columns added in
// migration 00020: owner / criticality / notes / runbook_url /
// annotations + hardware_model. Viewers and auditors see a read-only
// summary; editors and admins get an inline Edit form. Per ADR-0008,
// DICT data-classification is deliberately NOT on nodes — a node is
// infrastructure, not an application.

export function NodeCuratedCard({
  node,
  onSaved,
}: {
  node: api.Node;
  onSaved: () => void;
}) {
  const me = useMe();
  const [editing, setEditing] = useState(false);
  if (editing && canEdit(me)) {
    return (
      <NodeCuratedForm
        node={node}
        onCancel={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved();
        }}
      />
    );
  }
  const empty =
    !node.owner &&
    !node.criticality &&
    !node.notes &&
    !node.runbook_url &&
    !node.annotations &&
    !node.hardware_model;
  return (
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">Ownership &amp; context</h3>
        {canEdit(me) && (
          <button type="button" className="lv-btn lv-btn-ghost" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {empty ? (
        <p className="muted" style={{ marginTop: 0 }}>
          {canEdit(me)
            ? 'No curated metadata yet. Use Edit to record owner, criticality, hardware model, and a runbook link.'
            : 'No curated metadata recorded.'}
        </p>
      ) : (
        <dl className="kv-list">
          <KV k="Owner" v={node.owner} />
          <KV
            k="Criticality"
            v={
              node.criticality ? (
                <Pill status="accent">{node.criticality}</Pill>
              ) : undefined
            }
          />
          <KV k="Hardware model" v={node.hardware_model} />
          <KV
            k="Runbook"
            v={
              node.runbook_url ? (
                <a href={node.runbook_url} target="_blank" rel="noreferrer">
                  {node.runbook_url}
                </a>
              ) : undefined
            }
          />
          <KV
            k="Notes"
            v={node.notes ? <pre className="curated-notes">{node.notes}</pre> : undefined}
          />
          <KV k="Annotations" v={<Labels labels={node.annotations} />} />
        </dl>
      )}
    </div>
  );
}

function NodeCuratedForm({
  node,
  onCancel,
  onSaved,
}: {
  node: api.Node;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [owner, setOwner] = useState(node.owner || '');
  const [criticality, setCriticality] = useState(node.criticality || '');
  const [hardwareModel, setHardwareModel] = useState(node.hardware_model || '');
  const [notes, setNotes] = useState(node.notes || '');
  const [runbook, setRunbook] = useState(node.runbook_url || '');
  const [annotationsText, setAnnotationsText] = useState(formatKV(node.annotations));
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
      await api.updateNode(node.id, {
        owner: owner.trim(),
        criticality: criticality.trim(),
        hardware_model: hardwareModel.trim(),
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
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">Edit ownership &amp; context</h3>
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
          <div>
            <label>Hardware model</label>
            <input
              type="text"
              value={hardwareModel}
              onChange={(e) => setHardwareModel(e.target.value)}
              placeholder="Dell PowerEdge R640"
            />
          </div>
        </div>
        <div>
          <label>Runbook URL</label>
          <input
            type="url"
            value={runbook}
            onChange={(e) => setRunbook(e.target.value)}
            placeholder="https://runbooks.example.com/node-bm-01"
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
