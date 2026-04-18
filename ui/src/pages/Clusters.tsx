import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, Cluster, clearToken, listClusters } from '../api';

export default function Clusters() {
  const [items, setItems] = useState<Cluster[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    listClusters()
      .then((resp) => {
        if (!cancelled) setItems(resp.items);
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          // Token is invalid or rotated out — drop it and force re-login.
          clearToken();
          navigate('/login', { replace: true });
          return;
        }
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  if (error) {
    return (
      <>
        <h2>Clusters</h2>
        <div className="error">Failed to load: {error}</div>
      </>
    );
  }

  if (items === null) {
    return (
      <>
        <h2>Clusters</h2>
        <p className="loading">Loading…</p>
      </>
    );
  }

  if (items.length === 0) {
    return (
      <>
        <h2>Clusters</h2>
        <p className="muted">
          No clusters registered yet. Register one with{' '}
          <code>POST /v1/clusters</code>.
        </p>
      </>
    );
  }

  return (
    <>
      <h2>
        Clusters <span className="muted">({items.length})</span>
      </h2>
      <table className="entities">
        <thead>
          <tr>
            <th>Name</th>
            <th>Environment</th>
            <th>Provider</th>
            <th>Region</th>
            <th>K8s version</th>
            <th>Layer</th>
          </tr>
        </thead>
        <tbody>
          {items.map((c) => (
            <tr key={c.id}>
              <td>
                <strong>{c.display_name || c.name}</strong>
                {c.display_name && <div className="muted" style={{ fontSize: '0.8rem' }}>{c.name}</div>}
              </td>
              <td>{c.environment ?? <span className="muted">—</span>}</td>
              <td>{c.provider ?? <span className="muted">—</span>}</td>
              <td>{c.region ?? <span className="muted">—</span>}</td>
              <td>
                {c.kubernetes_version ? (
                  <code>{c.kubernetes_version}</code>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
              <td>
                <span className="pill">{c.layer}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
