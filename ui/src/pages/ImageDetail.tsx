import { Link, useParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView, Dash } from '../components';

// ImageDetail shows the per-repo drill-down: one row per tracked variant,
// with latest_tag, source, last-checked timestamp, and last_error if any.

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function ImageDetail() {
  // The route param is URL-encoded; decode before passing to the API.
  const { imageRepo = '' } = useParams<{ imageRepo: string }>();
  const decoded = decodeURIComponent(imageRepo);

  const ivState = useResource(() => api.getImageVersion(decoded), [decoded]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/images">Container images</Link> / <span>{decoded}</span>
      </div>

      <AsyncView state={ivState}>
        {(iv) => (
          <>
            <h2>{iv.image_repo}</h2>
            <p className="muted" style={{ marginBottom: '1rem' }}>
              Registry: <code>{iv.registry}</code>
            </p>

            {iv.variants.length === 0 ? (
              <p className="muted empty">No variants tracked for this image.</p>
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Variant</th>
                    <th>Latest tag</th>
                    <th>Source</th>
                    <th>Last checked</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {iv.variants.map((v, i) => (
                    <tr key={i} className={v.last_error ? 'vm-row-terminated' : ''}>
                      <td>
                        {v.variant ? (
                          <code>{v.variant}</code>
                        ) : (
                          <span className="muted">(default)</span>
                        )}
                      </td>
                      <td>
                        {v.latest_tag ? <code>{v.latest_tag}</code> : <Dash />}
                      </td>
                      <td>{v.source}</td>
                      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                        {formatTs(v.last_checked_at) || '—'}
                      </td>
                      <td>
                        {v.last_error ? (
                          <span
                            className="pill status-bad"
                            title={v.last_error}
                          >
                            error
                          </span>
                        ) : v.latest_tag ? (
                          <span className="pill status-ok">ok</span>
                        ) : (
                          <span className="pill">pending</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
    </>
  );
}
