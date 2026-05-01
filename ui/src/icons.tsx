// Design system SVG icons — 24x24 stroke outline, currentColor.
// Source: longue-vue Design System (assets/icons/*.svg).

interface IconProps {
  size?: number;
  className?: string;
  style?: React.CSSProperties;
}

const defaults = {
  fill: 'none' as const,
  stroke: 'currentColor',
  strokeWidth: 1.75,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
};

function Svg({ size = 16, className, style, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      className={className}
      style={{ verticalAlign: 'middle', flexShrink: 0, ...style }}
      {...defaults}
    >
      {children}
    </svg>
  );
}

export function ClusterIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <polygon points="12,2 21,7 21,17 12,22 3,17 3,7" />
      <circle cx={12} cy={12} r={2.2} />
      <line x1={12} y1={2} x2={12} y2={10} />
      <line x1={3.4} y1={7.5} x2={10} y2={11} />
      <line x1={20.6} y1={7.5} x2={14} y2={11} />
    </Svg>
  );
}

export function NodeIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <rect x={3} y={6} width={18} height={12} rx={1.5} />
      <line x1={3} y1={10} x2={21} y2={10} />
      <circle cx={6.5} cy={8} r={0.6} fill="currentColor" />
      <circle cx={8.5} cy={8} r={0.6} fill="currentColor" />
    </Svg>
  );
}

export function NamespaceIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <rect x={4} y={4} width={16} height={16} rx={2} />
      <line x1={4} y1={9} x2={20} y2={9} />
      <line x1={9} y1={4} x2={9} y2={20} />
    </Svg>
  );
}

export function WorkloadIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <polygon points="12,2 21,7 12,12 3,7" />
      <polyline points="3,12 12,17 21,12" />
      <polyline points="3,17 12,22 21,17" />
    </Svg>
  );
}

export function PodIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx={12} cy={12} r={8} />
      <circle cx={12} cy={12} r={3} />
      <line x1={12} y1={4} x2={12} y2={8} />
      <line x1={12} y1={16} x2={12} y2={20} />
      <line x1={4} y1={12} x2={8} y2={12} />
      <line x1={16} y1={12} x2={20} y2={12} />
    </Svg>
  );
}

export function ServiceIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx={12} cy={12} r={3} />
      <circle cx={4} cy={6} r={2} />
      <circle cx={4} cy={18} r={2} />
      <circle cx={20} cy={12} r={2} />
      <line x1={6} y1={6} x2={9.5} y2={10.5} />
      <line x1={6} y1={18} x2={9.5} y2={13.5} />
      <line x1={15} y1={12} x2={18} y2={12} />
    </Svg>
  );
}

export function IngressIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx={12} cy={12} r={9} />
      <line x1={3} y1={12} x2={21} y2={12} />
      <path d="M12 3 Q 6 12 12 21" />
      <path d="M12 3 Q 18 12 12 21" />
    </Svg>
  );
}

export function VolumeIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <ellipse cx={12} cy={5} rx={8} ry={2.5} />
      <path d="M4 5 v14 a8 2.5 0 0 0 16 0 v-14" />
      <path d="M4 12 a8 2.5 0 0 0 16 0" />
    </Svg>
  );
}

export function SearchIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx={11} cy={11} r={6} />
      <line x1={15.5} y1={15.5} x2={21} y2={21} />
    </Svg>
  );
}

export function EolIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M12 2 v4" />
      <path d="M12 18 v4" />
      <path d="M2 12 h4" />
      <path d="M18 12 h4" />
      <circle cx={12} cy={12} r={5} />
      <path d="M5 5 l2.5 2.5" />
      <path d="M16.5 16.5 l2.5 2.5" />
      <path d="M5 19 l2.5 -2.5" />
      <path d="M16.5 7.5 l2.5 -2.5" />
    </Svg>
  );
}

// VirtualMachineIcon — server / tower glyph: stacked rack units with a
// status LED, distinct from NodeIcon (which depicts a horizontal blade).
export function VirtualMachineIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <rect x={5} y={3} width={14} height={18} rx={1.5} />
      <line x1={5} y1={9} x2={19} y2={9} />
      <line x1={5} y1={15} x2={19} y2={15} />
      <circle cx={8} cy={6} r={0.6} fill="currentColor" />
      <line x1={11} y1={6} x2={16} y2={6} />
      <circle cx={8} cy={12} r={0.6} fill="currentColor" />
      <line x1={11} y1={12} x2={16} y2={12} />
      <circle cx={8} cy={18} r={0.6} fill="currentColor" />
      <line x1={11} y1={18} x2={16} y2={18} />
    </Svg>
  );
}

// CloudAccountIcon — admin-tab glyph for cloud-provider account rows.
export function CloudAccountIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M7 16 a4 4 0 0 1 0.5 -7.95 a5 5 0 0 1 9.5 1.5 a3.5 3.5 0 0 1 -0.5 6.95 z" />
      <line x1={9} y1={13} x2={15} y2={13} />
    </Svg>
  );
}

export function AdminIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M12 3 L4 6 V12 C4 17 8 20 12 21 C16 20 20 17 20 12 V6 Z" />
      <path d="M9 12 l2 2 l4 -4" />
    </Svg>
  );
}

// Entity type → icon lookup for tables and headers.
const ENTITY_ICONS: Record<string, React.FC<IconProps>> = {
  cluster: ClusterIcon,
  node: NodeIcon,
  namespace: NamespaceIcon,
  workload: WorkloadIcon,
  pod: PodIcon,
  service: ServiceIcon,
  ingress: IngressIcon,
  persistentvolume: VolumeIcon,
  persistentvolumeclaim: VolumeIcon,
  virtual_machine: VirtualMachineIcon,
  cloud_account: CloudAccountIcon,
};

export function EntityIcon({ type, ...props }: IconProps & { type: string }) {
  const Icon = ENTITY_ICONS[type];
  return Icon ? <Icon {...props} /> : null;
}
