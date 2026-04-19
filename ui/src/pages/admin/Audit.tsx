import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';

// Admin Audit page. Read-only, newest-first list of recorded API
// actions. Auditors can reach this without the rest of the admin panel
// (see AdminLayout + RequireAdmin). Filters are applied server-side so
// the page stays fast with large event counts.

type FilterForm = {
  actor: string;
  resourceType: string;
  action: string;
  since: string;
};

const emptyFilter: FilterForm = { actor: '', resourceType: '', action: '', since: '' };

export default function AuditPage() {
  const [draft, setDraft] = useState<FilterForm>(emptyFilter);
  const [applied, setApplied] = useState<FilterForm>(emptyFilter);
  const state = useResource(
    () =>
      api.listAuditEvents({
        actorId: applied.actor || undefined,
        resourceType: applied.resourceType || undefined,
        action: applied.action || undefined,
        since: applied.since ? new Date(applied.since).toISOString() : undefined,
      }),
    [applied],
  );

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setApplied(draft);
  };
  const clear = () => {
    setDraft(emptyFilter);
    setApplied(emptyFilter);
  };

  return (
    <>
      <SectionTitle>Audit log</SectionTitle>
      <p className="muted" style={{ fontSize: '0.85rem', marginTop: 0 }}>
        Every state-changing call and every admin-panel read is recorded here.
        Rows are immutable; filter by actor, resource kind, or action verb.
      </p>

      <form className="search-form" onSubmit={onSubmit} style={{ flexWrap: 'wrap' }}>
        <input
          type="text"
          placeholder="Actor id (UUID)"
          value={draft.actor}
          onChange={(e) => setDraft({ ...draft, actor: e.target.value })}
          style={{ flex: '1 1 200px' }}
        />
        <input
          type="text"
          placeholder="Resource type (e.g. cluster, admin_user)"
          value={draft.resourceType}
          onChange={(e) => setDraft({ ...draft, resourceType: e.target.value })}
          style={{ flex: '1 1 200px' }}
        />
        <input
          type="text"
          placeholder="Action (e.g. user.create)"
          value={draft.action}
          onChange={(e) => setDraft({ ...draft, action: e.target.value })}
          style={{ flex: '1 1 180px' }}
        />
        <input
          type="datetime-local"
          value={draft.since}
          onChange={(e) => setDraft({ ...draft, since: e.target.value })}
          style={{ flex: '1 1 200px' }}
        />
        <button type="submit">Apply</button>
        <button type="button" onClick={clear} className="danger">
          Clear
        </button>
      </form>

      <AsyncView state={state}>
        {(resp) =>
          resp.items.length === 0 ? (
            <p className="muted">No audit events match.</p>
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Actor</th>
                  <th>Action</th>
                  <th>Resource</th>
                  <th>HTTP</th>
                  <th>Source IP</th>
                </tr>
              </thead>
              <tbody>
                {resp.items.map((ev) => (
                  <AuditRow key={ev.id} ev={ev} />
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </>
  );
}

function AuditRow({ ev }: { ev: api.AuditEvent }) {
  return (
    <tr>
      <td>
        <span className="muted" style={{ fontSize: '0.8rem' }}>
          {formatTs(ev.occurred_at)}
        </span>
      </td>
      <td>
        {ev.actor_username ? (
          <>
            <strong>{ev.actor_username}</strong>{' '}
            {ev.actor_role && <span className="pill">{ev.actor_role}</span>}{' '}
            <span className="muted" style={{ fontSize: '0.75rem' }}>
              ({ev.actor_kind})
            </span>
          </>
        ) : (
          <span className="muted">{ev.actor_kind}</span>
        )}
      </td>
      <td>
        <code className="inline-code">{ev.action}</code>
      </td>
      <td>
        {ev.resource_type ? (
          <>
            <code className="inline-code">{ev.resource_type}</code>
            {ev.resource_id ? (
              <span className="muted" style={{ fontSize: '0.8rem', marginLeft: '0.4rem' }}>
                {shortRes(ev.resource_id)}
              </span>
            ) : null}
          </>
        ) : (
          <Dash />
        )}
      </td>
      <td>
        <span className={statusClass(ev.http_status)}>
          {ev.http_method} {ev.http_status}
        </span>
      </td>
      <td>{ev.source_ip ? <code>{ev.source_ip}</code> : <Dash />}</td>
    </tr>
  );
}

function statusClass(status: number): string {
  if (status >= 500) return 'pill status-bad';
  if (status >= 400) return 'pill status-warn';
  return 'pill status-ok';
}

function shortRes(id: string): string {
  // UUIDs get the short-form treatment so the column doesn't jump.
  if (/^[0-9a-f-]{8}-[0-9a-f-]{4}/.test(id) && id.length > 16) {
    return id.slice(0, 8) + '…';
  }
  return id;
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}
