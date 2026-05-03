import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Accent = 'cyan' | 'amber' | 'sage' | 'coral' | 'violet';
export type Density = 'compact' | 'standard' | 'comfortable';
export type PillStyle = 'solid' | 'outline' | 'dot';

export type UiPrefs = {
  accent: Accent;
  density: Density;
  pillStyle: PillStyle;
};

export const DEFAULTS: UiPrefs = { accent: 'cyan', density: 'standard', pillStyle: 'solid' };
export const STORAGE_KEY = 'lv:ui-prefs';

const ACCENTS: ReadonlySet<Accent> = new Set(['cyan', 'amber', 'sage', 'coral', 'violet']);
const DENSITIES: ReadonlySet<Density> = new Set(['compact', 'standard', 'comfortable']);
const PILL_STYLES: ReadonlySet<PillStyle> = new Set(['solid', 'outline', 'dot']);

function readPrefs(): UiPrefs {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { ...DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<UiPrefs>;
    return {
      accent: ACCENTS.has(parsed.accent as Accent) ? (parsed.accent as Accent) : DEFAULTS.accent,
      density: DENSITIES.has(parsed.density as Density) ? (parsed.density as Density) : DEFAULTS.density,
      pillStyle: PILL_STYLES.has(parsed.pillStyle as PillStyle) ? (parsed.pillStyle as PillStyle) : DEFAULTS.pillStyle,
    };
  } catch {
    return { ...DEFAULTS };
  }
}

function writeBodyDataset(p: UiPrefs) {
  document.body.dataset.accent = p.accent;
  document.body.dataset.density = p.density;
  document.body.dataset.pillStyle = p.pillStyle;
}

// Called once from main.tsx before React renders so the login splash already
// reflects the user's accent / density / pill choice before the provider mounts.
export function bootstrapBodyDataset(): void {
  writeBodyDataset(readPrefs());
}

type Ctx = { prefs: UiPrefs; setPref: <K extends keyof UiPrefs>(key: K, value: UiPrefs[K]) => void };

const UiPrefsContext = createContext<Ctx | null>(null);

export function UiPrefsProvider({ children }: { children: ReactNode }) {
  const [prefs, setPrefs] = useState<UiPrefs>(readPrefs);

  useEffect(() => {
    writeBodyDataset(prefs);
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
    } catch (err) {
      console.warn('UI preferences not persisted:', err);
    }
  }, [prefs]);

  const setPref = useCallback(<K extends keyof UiPrefs>(key: K, value: UiPrefs[K]) => {
    setPrefs((p) => ({ ...p, [key]: value }));
  }, []);

  const value = useMemo(() => ({ prefs, setPref }), [prefs, setPref]);
  return <UiPrefsContext.Provider value={value}>{children}</UiPrefsContext.Provider>;
}

export function useUiPrefs(): Ctx {
  const ctx = useContext(UiPrefsContext);
  if (!ctx) throw new Error('useUiPrefs must be used inside <UiPrefsProvider>');
  return ctx;
}
