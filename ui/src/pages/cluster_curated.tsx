import { FormEvent, useState } from 'react';
import * as api from '../api';
import { canEdit, useMe } from '../me';
import { KV, Labels } from '../components';
import { Pill } from '../components/lv/Pill';
import { formatKV, parseKV } from '../kv';

// ClusterCuratedCard renders the operator-owned fields that don't come
// from the Kubernetes API (owner / criticality / notes / runbook /
// annotations). Viewers and auditors see a read-only summary; editors
// and admins get an inline Edit button that flips to a form. Saving
// calls updateCluster with a merge-patch body and bubbles reload to
// the parent.
export function ClusterCuratedCard({
  cluster,
  onSaved,
}: {
  cluster: api.Cluster;
  onSaved: () => void;
}) {
  const me = useMe();
  const [editing, setEditing] = useState(false);
  if (editing && canEdit(me)) {
    return (
      <ClusterCuratedForm
        cluster={cluster}
        onCancel={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved();
        }}
      />
    );
  }
  const empty =
    !cluster.owner &&
    !cluster.criticality &&
    !cluster.notes &&
    !cluster.runbook_url &&
    !cluster.annotations;
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
            ? 'No curated metadata yet. Use Edit to record owner, criticality, and a runbook link.'
            : 'No curated metadata recorded.'}
        </p>
      ) : (
        <dl className="kv-list">
          <KV k="Owner" v={cluster.owner} />
          <KV
            k="Criticality"
            v={
              cluster.criticality ? (
                <Pill status="accent">{cluster.criticality}</Pill>
              ) : undefined
            }
          />
          <KV
            k="Runbook"
            v={
              cluster.runbook_url ? (
                <a href={cluster.runbook_url} target="_blank" rel="noreferrer">
                  {cluster.runbook_url}
                </a>
              ) : undefined
            }
          />
          <KV
            k="Notes"
            v={
              cluster.notes ? (
                <pre className="curated-notes">{cluster.notes}</pre>
              ) : undefined
            }
          />
          <KV k="Annotations" v={<Labels labels={cluster.annotations} />} />
        </dl>
      )}
    </div>
  );
}

function ClusterCuratedForm({
  cluster,
  onCancel,
  onSaved,
}: {
  cluster: api.Cluster;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [environment, setEnvironment] = useState(cluster.environment || '');
  const [provider, setProvider] = useState(cluster.provider || '');
  const [region, setRegion] = useState(cluster.region || '');
  const [labelsText, setLabelsText] = useState(formatKV(cluster.labels));
  const [owner, setOwner] = useState(cluster.owner || '');
  const [criticality, setCriticality] = useState(cluster.criticality || '');
  const [notes, setNotes] = useState(cluster.notes || '');
  const [runbook, setRunbook] = useState(cluster.runbook_url || '');
  const [annotationsText, setAnnotationsText] = useState(formatKV(cluster.annotations));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    // Parse labels + annotations as `key=value` one per line before
    // PATCHing, so the user doesn't see JSON-parse errors for a
    // trailing comma.
    let labels: Record<string, string>;
    let annotations: Record<string, string>;
    try {
      labels = parseKV(labelsText, 'labels');
      annotations = parseKV(annotationsText, 'annotations');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return;
    }
    setBusy(true);
    try {
      if (!cluster.id) throw new Error('cluster missing id');
      await api.updateCluster(cluster.id, {
        environment: environment.trim(),
        provider: provider.trim(),
        region: region.trim(),
        labels: labels,
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
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">Edit ownership &amp; context</h3>
      </div>
      <form className="admin-form" onSubmit={onSubmit}>
        <div className="admin-form-row">
          <div>
            <label>Environment</label>
            <input
              type="text"
              value={environment}
              onChange={(e) => setEnvironment(e.target.value)}
              placeholder="dev / staging / prod"
            />
          </div>
          <div>
            <label>Provider</label>
            <input
              type="text"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              placeholder="gke / eks / aks / openshift / onprem"
            />
          </div>
          <div>
            <label>Region</label>
            <input
              type="text"
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              placeholder="eu-west-1 / paris-a"
            />
          </div>
        </div>
        <div style={{ marginTop: '0.75rem' }}>
          <label>Labels (one key=value per line)</label>
          <textarea
            value={labelsText}
            onChange={(e) => setLabelsText(e.target.value)}
            rows={3}
            style={{
              width: '100%',
              fontFamily: 'ui-monospace, SFMono-Regular, Consolas, monospace',
            }}
          />
        </div>

        <div className="admin-form-row" style={{ marginTop: '0.75rem' }}>
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
            placeholder="https://runbooks.example.com/prod-cluster"
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

// formatKV / parseKV serialize a `Record<string, string>` as one
// `key=value` per line. Used for both `labels` and `annotations` — the
// shapes are identical, just different JSONB columns server-side.
