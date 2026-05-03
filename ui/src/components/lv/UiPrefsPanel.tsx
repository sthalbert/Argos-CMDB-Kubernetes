import { useUiPrefs, type Accent, type Density, type PillStyle } from '../../ui-prefs';

const ACCENTS: Accent[] = ['cyan', 'amber', 'sage', 'coral', 'violet'];
const DENSITIES: Density[] = ['compact', 'standard', 'comfortable'];
const PILL_STYLES: PillStyle[] = ['solid', 'outline', 'dot'];

export function UiPrefsPanel() {
  const { prefs, setPref } = useUiPrefs();
  return (
    <div>
      <div className="lv-popover-section-label">Accent</div>
      {ACCENTS.map((a) => (
        <label key={a} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-accent"
            value={a}
            checked={prefs.accent === a}
            onChange={() => setPref('accent', a)}
          />
          {a}
        </label>
      ))}
      <div className="lv-popover-divider" />
      <div className="lv-popover-section-label">Density</div>
      {DENSITIES.map((d) => (
        <label key={d} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-density"
            value={d}
            checked={prefs.density === d}
            onChange={() => setPref('density', d)}
          />
          {d}
        </label>
      ))}
      <div className="lv-popover-divider" />
      <div className="lv-popover-section-label">Pill style</div>
      {PILL_STYLES.map((p) => (
        <label key={p} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-pill-style"
            value={p}
            checked={prefs.pillStyle === p}
            onChange={() => setPref('pillStyle', p)}
          />
          {p}
        </label>
      ))}
    </div>
  );
}
