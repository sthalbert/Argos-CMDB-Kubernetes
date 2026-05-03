type SpokeProps = { cx: number; cy: number; r: number; hub: number; strokeWidth?: number };

function Spokes({ cx, cy, r, hub, strokeWidth = 1.5 }: SpokeProps) {
  const out = [];
  for (let i = 0; i < 7; i++) {
    const a = (i / 7) * Math.PI * 2 - Math.PI / 2;
    const x = cx + Math.cos(a) * r;
    const y = cy + Math.sin(a) * r;
    const xi = cx + Math.cos(a) * hub;
    const yi = cy + Math.sin(a) * hub;
    out.push(<line key={i} x1={xi} y1={yi} x2={x} y2={y} strokeWidth={strokeWidth} />);
  }
  return <>{out}</>;
}

function heptagon(cx: number, cy: number, r: number): string {
  return Array.from({ length: 7 }, (_, i) => {
    const a = (i / 7) * Math.PI * 2 - Math.PI / 2;
    return `${(cx + Math.cos(a) * r).toFixed(2)},${(cy + Math.sin(a) * r).toFixed(2)}`;
  }).join(' ');
}

export function Logomark({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <circle cx={16} cy={16} r={13} strokeWidth={1.75} />
      <polygon points={heptagon(16, 16, 8)} strokeWidth={1.5} />
      <Spokes cx={16} cy={16} r={8} hub={2.5} strokeWidth={1.5} />
      <circle cx={16} cy={16} r={1.6} strokeWidth={1.5} fill="currentColor" />
    </svg>
  );
}

export function LogomarkLarge({ size = 180, className }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 180 180"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <circle cx={90} cy={90} r={75} strokeWidth={1.5} />
      <circle cx={90} cy={90} r={62} strokeWidth={0.75} opacity={0.35} />
      <polygon points={heptagon(90, 90, 50)} strokeWidth={1.5} />
      <Spokes cx={90} cy={90} r={50} hub={14} strokeWidth={1.5} />
      <circle cx={90} cy={90} r={8} strokeWidth={1.5} fill="currentColor" />
      <line x1={15} y1={90} x2={165} y2={90} strokeWidth={0.5} opacity={0.2} />
      <line x1={90} y1={15} x2={90} y2={165} strokeWidth={0.5} opacity={0.2} />
    </svg>
  );
}
