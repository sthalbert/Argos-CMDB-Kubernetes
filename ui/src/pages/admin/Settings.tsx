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
  const [saving, setSaving] = useState<string | null>(null);
  const [error, setError] = useState('');

  const toggle = async (field: 'eol_enabled' | 'mcp_enabled' | 'image_versions_enabled') => {
    setSaving(field);
    setError('');
    try {
      await api.updateSettings({ [field]: !settings[field] });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update settings');
    } finally {
      setSaving(null);
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <SettingToggle
        label="End-of-Life enrichment"
        description="Periodically queries endoflife.date and annotates clusters and nodes with lifecycle status (EOL, approaching EOL, supported)."
        enabled={settings.eol_enabled}
        saving={saving === 'eol_enabled'}
        onToggle={() => toggle('eol_enabled')}
      />
      <SettingToggle
        label="MCP server"
        description="Exposes the CMDB as read-only tools via the Model Context Protocol. AI agents can query clusters, nodes, pods, workloads, and more."
        enabled={settings.mcp_enabled}
        saving={saving === 'mcp_enabled'}
        onToggle={() => toggle('mcp_enabled')}
      />
      <SettingToggle
        label="Image versions enrichment"
        description="Periodically queries public registries for the latest version of each image used in your clusters. Configure registries under the Image registries tab."
        enabled={settings.image_versions_enabled}
        saving={saving === 'image_versions_enabled'}
        onToggle={() => toggle('image_versions_enabled')}
      />
      <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        Last updated: {new Date(settings.updated_at).toLocaleString()}
      </div>
      {error && <div className="error" style={{ marginTop: '0.5rem' }}>{error}</div>}
    </div>
  );
}

function SettingToggle({
  label,
  description,
  enabled,
  saving,
  onToggle,
}: {
  label: string;
  description: string;
  enabled: boolean;
  saving: boolean;
  onToggle: () => void;
}) {
  return (
    <section className="admin-form" style={{ maxWidth: 500 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '1rem' }}>
        <div>
          <strong>{label}</strong>
          <p className="muted" style={{ margin: '0.25rem 0 0', fontSize: 'var(--fs-base)' }}>
            {description}
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <span className={`pill ${enabled ? 'status-ok' : ''}`}>
            {enabled ? 'Enabled' : 'Disabled'}
          </span>
          <button
            className={enabled ? 'danger' : 'primary'}
            onClick={onToggle}
            disabled={saving}
          >
            {saving ? 'Saving…' : enabled ? 'Disable' : 'Enable'}
          </button>
        </div>
      </div>
    </section>
  );
}
