import * as api from '../api';
import { Dash } from '../components';
import { EntityListPage } from '../components/EntityListPage';
import { PolicyIcon } from '../icons';

function ReadyDot({ ready }: { ready?: boolean | null }) {
  if (ready === null || ready === undefined)
    return <span className="muted" title="Unknown">—</span>;
  return ready ? (
    <span className="pill status-ok" title="Ready">Ready</span>
  ) : (
    <span className="pill status-bad" title="Not ready">Not ready</span>
  );
}

function SeverityPill({ severity }: { severity?: string | null }) {
  if (!severity) return <Dash />;
  const cls: Record<string, string> = {
    critical: 'status-bad',
    high: 'status-bad',
    medium: 'status-warn',
    low: 'status-ok',
    info: 'status-ok',
  };
  return <span className={`pill ${cls[severity.toLowerCase()] || ''}`}>{severity}</span>;
}

// PoliciesDisabledError marks the 409 the API answers while
// policies_enabled is off, so errorRenderer can swap the generic error
// for the banner. Same pattern as FlowMatrixDisabledError in api/flows.ts.
class PoliciesDisabledError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PoliciesDisabledError';
  }
}

function withDisabledCatch<T>(
  fetcher: (params: api.ListControlParams, cursor: string | undefined, limit: number) => Promise<api.PagedResponse<T>>,
): (params: api.ListControlParams, cursor: string | undefined, limit: number) => Promise<api.PagedResponse<T>> {
  return async (params, cursor, limit) => {
    try {
      return await fetcher(params, cursor, limit);
    } catch (err) {
      if (err instanceof api.ApiError && err.status === 409) {
        throw new PoliciesDisabledError(err.message);
      }
      throw err;
    }
  };
}

function isFeatureDisabledError(err: unknown): boolean {
  return err instanceof PoliciesDisabledError;
}

function FeatureDisabledBanner() {
  return (
    <div className="banner banner-warn">
      <span>
        The Kyverno policies feature is disabled. Enable{' '}
        <strong>policies_enabled</strong> in <strong>Admin &gt; Settings</strong>{' '}
        to use this tool.
      </span>
    </div>
  );
}

export function ClusterPolicies() {
  return (
    <EntityListPage<api.ClusterPolicy>
      title="Cluster Policies"
      icon={<PolicyIcon size={20} />}
      storageKey="lists.cluster-policies"
      emptyMessage="No Kyverno policies found. Ensure the collector is running and policies_enabled is on."
      fetchPage={withDisabledCatch((params, cursor, limit) =>
        api.listClusterPolicies({ ...params, cursor, limit })
      )}
      rowKey={(c) => c.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (c) => <strong>{c.name}</strong>,
        },
        {
          key: 'resource_type',
          label: 'Kind',
          sortKey: 'resource_type',
          render: (c) => <code>{c.resource_type}</code>,
        },
        {
          key: 'category',
          label: 'Category',
          sortKey: 'category',
          render: (c) => c.category || <Dash />,
        },
        {
          key: 'severity',
          label: 'Severity',
          sortKey: 'severity',
          render: (c) => <SeverityPill severity={c.severity} />,
        },
        {
          key: 'action',
          label: 'Action',
          sortKey: 'action',
          render: (c) =>
            c.action ? <span className="pill">{c.action}</span> : <Dash />,
        },
        {
          key: 'failure_policy',
          label: 'Failure Policy',
          sortKey: 'failure_policy',
          render: (c) => c.failure_policy || <Dash />,
        },
        {
          key: 'background',
          label: 'Background',
          sortKey: 'background',
          render: (c) =>
            c.background != null ? (c.background ? 'yes' : 'no') : <Dash />,
        },
        {
          key: 'rules_count',
          label: 'Rules',
          sortKey: 'rules_count',
          render: (c) => c.rules_count ?? <Dash />,
        },
        {
          key: 'ready',
          label: 'Ready',
          sortKey: 'ready',
          render: (c) => <ReadyDot ready={c.ready} />,
        },
      ]}
      errorRenderer={(err) =>
        isFeatureDisabledError(err) ? <FeatureDisabledBanner /> : undefined
      }
    />
  );
}

export function PolicyReports() {
  return (
    <EntityListPage<api.PolicyReport>
      title="Policy Reports"
      icon={<PolicyIcon size={20} />}
      storageKey="lists.policy-reports"
      emptyMessage="No policy reports found. Ensure the collector is running and policies_enabled is on."
      fetchPage={withDisabledCatch((params, cursor, limit) =>
        api.listPolicyReports({ ...params, cursor, limit })
      )}
      rowKey={(r) => r.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (r) => <strong>{r.name}</strong>,
        },
        {
          key: 'scope_kind',
          label: 'Scope Kind',
          sortKey: 'scope_kind',
          render: (r) => r.scope_kind || <Dash />,
        },
        {
          key: 'scope_name',
          label: 'Scope Name',
          sortKey: 'scope_name',
          render: (r) => r.scope_name || <Dash />,
        },
        {
          key: 'pass',
          label: 'Pass',
          sortKey: 'summary_pass',
          render: (r) => r.summary_pass ?? 0,
        },
        {
          key: 'fail',
          label: 'Fail',
          sortKey: 'summary_fail',
          render: (r) =>
            r.summary_fail ? (
              <span className="pill status-bad">{r.summary_fail}</span>
            ) : (
              0
            ),
        },
        {
          key: 'warn',
          label: 'Warn',
          sortKey: 'summary_warn',
          render: (r) =>
            r.summary_warn ? (
              <span className="pill status-warn">{r.summary_warn}</span>
            ) : (
              0
            ),
        },
        {
          key: 'error',
          label: 'Error',
          sortKey: 'summary_error',
          render: (r) =>
            r.summary_error ? (
              <span className="pill status-bad">{r.summary_error}</span>
            ) : (
              0
            ),
        },
        {
          key: 'skip',
          label: 'Skip',
          sortKey: 'summary_skip',
          render: (r) => r.summary_skip ?? 0,
        },
      ]}
      errorRenderer={(err) =>
        isFeatureDisabledError(err) ? <FeatureDisabledBanner /> : undefined
      }
    />
  );
}
