import { FormEvent, useState } from 'react';
import type { ReactNode } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';
import { AuditHeader, AuditRow } from '../../components/lv/AuditRow';
import { Pill } from '../../components/lv/Pill';

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
        <button type="submit" className="lv-btn lv-btn-primary">Apply</button>
        <button type="button" onClick={clear} className="lv-btn lv-btn-ghost">
          Clear
        </button>
      </form>

      <AsyncView state={state}>
        {(resp) =>
          resp.items.length === 0 ? (
            <p className="muted">No audit events match.</p>
          ) : (
            <div role="table">
              <AuditHeader />
              {resp.items.map((e) => (
                <AuditRow
                  key={e.id}
                  time={formatTs(e.occurred_at)}
                  actor={actorLabel(e)}
                  message={renderMessage(e)}
                  result={statusPill(e.http_status)}
                  meta={e.source_ip ? <code>{e.source_ip}</code> : <Dash />}
                />
              ))}
            </div>
          )
        }
      </AsyncView>
    </>
  );
}

function actorLabel(e: api.AuditEvent): ReactNode {
  const name = e.actor_username ?? e.actor_id ?? e.actor_kind;
  if (!e.actor_role) return name;
  return (
    <>
      {name} <Pill>{e.actor_role}</Pill>
    </>
  );
}

function renderMessage(e: api.AuditEvent): ReactNode {
  const parts: ReactNode[] = [<code key="action">{e.action}</code>];
  if (e.resource_type) {
    parts.push(' ');
    parts.push(<code key="rt">{e.resource_type}</code>);
  }
  if (e.resource_id) {
    parts.push(' ');
    parts.push(
      <span key="rid" className="muted" style={{ fontSize: '0.8rem' }}>
        {shortRes(e.resource_id)}
      </span>,
    );
  }
  // Include the HTTP method and path for context.
  parts.push(
    <span key="http" className="muted" style={{ fontSize: '0.8rem', marginLeft: '0.5rem' }}>
      {e.http_method} {e.http_path}
    </span>,
  );
  return <>{parts}</>;
}

function statusPill(status: number): ReactNode {
  if (status >= 500) return <Pill status="bad">{status}</Pill>;
  if (status >= 400) return <Pill status="warn">{status}</Pill>;
  return <Pill status="ok">{status}</Pill>;
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
