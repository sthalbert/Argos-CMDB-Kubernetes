export type TabItem = { key: string; label: string };

export function Tabs({
  items,
  active,
  onChange,
  className,
}: {
  items: TabItem[];
  active: string;
  onChange: (key: string) => void;
  className?: string;
}) {
  return (
    <div role="tablist" className={['lv-tabs', className].filter(Boolean).join(' ')}>
      {items.map((it) => {
        const isActive = it.key === active;
        return (
          <button
            key={it.key}
            type="button"
            role="tab"
            aria-selected={isActive}
            className={['lv-tab', isActive && 'active'].filter(Boolean).join(' ')}
            onClick={() => onChange(it.key)}
          >
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
