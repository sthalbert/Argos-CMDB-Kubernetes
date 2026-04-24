import { useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView } from '../../components';

export default function SettingsPage() {
  const [nonce, setNonce] = useState(0);
  const state = useResource(() => api.getSettings(), [nonce]);

  return (
    <>
      <h3>Settings</h3>
      <AsyncView state={state}>
        {(settings) => <SettingsForm settings={settings} onSaved={() => setNonce((n) => n + 1)} />}
      </AsyncView>
    </>
  );
}

function SettingsForm({
  settings,
  onSaved,
}: {
  settings: api.Settings;
  onSaved: () => void;
}) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const toggle = async () => {
    setSaving(true);
    setError('');
    try {
      await api.updateSettings({ eol_enabled: !settings.eol_enabled });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update settings');
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="admin-form" style={{ maxWidth: 500 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '1rem' }}>
        <div>
          <strong>End-of-Life enrichment</strong>
          <p className="muted" style={{ margin: '0.25rem 0 0', fontSize: '0.85rem' }}>
            Periodically queries endoflife.date and annotates clusters and nodes
            with lifecycle status (EOL, approaching EOL, supported).
          </p>
        </div>
        <button
          className={settings.eol_enabled ? 'danger' : 'primary'}
          onClick={toggle}
          disabled={saving}
        >
          {saving ? 'Saving…' : settings.eol_enabled ? 'Disable' : 'Enable'}
        </button>
      </div>
      <div style={{ marginTop: '0.75rem' }}>
        <span className={`pill ${settings.eol_enabled ? 'status-ok' : ''}`}>
          {settings.eol_enabled ? 'Enabled' : 'Disabled'}
        </span>
        <span className="muted" style={{ marginLeft: '0.75rem', fontSize: '0.8rem' }}>
          Last updated: {new Date(settings.updated_at).toLocaleString()}
        </span>
      </div>
      {error && <div className="error" style={{ marginTop: '0.5rem' }}>{error}</div>}
    </section>
  );
}
