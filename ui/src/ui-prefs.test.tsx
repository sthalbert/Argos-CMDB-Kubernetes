import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, act } from '@testing-library/react';
import { UiPrefsProvider, useUiPrefs, bootstrapBodyDataset, STORAGE_KEY, DEFAULTS } from './ui-prefs';

const Probe = () => {
  const { prefs, setPref } = useUiPrefs();
  return (
    <div>
      <span data-testid="accent">{prefs.accent}</span>
      <span data-testid="density">{prefs.density}</span>
      <span data-testid="pillStyle">{prefs.pillStyle}</span>
      <button onClick={() => setPref('accent', 'amber')}>amber</button>
      <button onClick={() => setPref('density', 'compact')}>compact</button>
      <button onClick={() => setPref('pillStyle', 'dot')}>dot</button>
    </div>
  );
};

describe('UiPrefs', () => {
  beforeEach(() => {
    localStorage.clear();
    document.body.removeAttribute('data-accent');
    document.body.removeAttribute('data-density');
    document.body.removeAttribute('data-pill-style');
  });

  it('falls back to DEFAULTS when localStorage is empty', () => {
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe(DEFAULTS.accent);
    expect(getByTestId('density').textContent).toBe(DEFAULTS.density);
    expect(getByTestId('pillStyle').textContent).toBe(DEFAULTS.pillStyle);
  });

  it('reads persisted prefs from localStorage on mount', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'sage', density: 'compact', pillStyle: 'outline' }));
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe('sage');
    expect(getByTestId('density').textContent).toBe('compact');
    expect(getByTestId('pillStyle').textContent).toBe('outline');
  });

  it('sets body data-attributes on mount and on change', () => {
    const { getByText } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(document.body.dataset.accent).toBe(DEFAULTS.accent);
    act(() => { getByText('amber').click(); });
    expect(document.body.dataset.accent).toBe('amber');
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!).accent).toBe('amber');
  });

  it('shallow-merges unknown / partial localStorage payloads', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'violet', extra: 'ignored' }));
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe('violet');
    expect(getByTestId('density').textContent).toBe(DEFAULTS.density);
  });

  it('survives a localStorage write throw without crashing', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { getByText } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(() => act(() => { getByText('compact').click(); })).not.toThrow();
    expect(warn).toHaveBeenCalled();
    setItem.mockRestore();
    warn.mockRestore();
  });

  it('bootstrapBodyDataset applies persisted prefs synchronously', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'coral', density: 'comfortable', pillStyle: 'dot' }));
    bootstrapBodyDataset();
    expect(document.body.dataset.accent).toBe('coral');
    expect(document.body.dataset.density).toBe('comfortable');
    expect(document.body.dataset.pillStyle).toBe('dot');
  });

  it('bootstrapBodyDataset applies DEFAULTS when no persisted prefs', () => {
    bootstrapBodyDataset();
    expect(document.body.dataset.accent).toBe(DEFAULTS.accent);
    expect(document.body.dataset.pillStyle).toBe(DEFAULTS.pillStyle);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });
});
