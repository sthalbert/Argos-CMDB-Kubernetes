import {
  useState,
  useMemo,
  useRef,
  useEffect,
  useCallback,
  MouseEvent as ReactMouseEvent,
} from 'react';
import { useNavigate } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView } from '../components';
import { Tabs } from '../components/lv/Tabs';

// --- Layout constants -------------------------------------------------------

const NODE_W = 180;
const NODE_H = 50;
const H_GAP = 24;
const V_GAP = 70;
const GROUP_THRESHOLD = 6;
const MIN_SCALE = 0.25;
const MAX_SCALE = 4;
const DRAG_THRESHOLD_PX = 3;

// --- Entity type metadata ---------------------------------------------------

const TYPE_META: Record<
  api.ImpactEntityType,
  { label: string; pluralLabel: string; color: string; path: string }
> = {
  cluster:               { label: 'Cluster',   pluralLabel: 'clusters',   color: '#5cc8ff', path: '/clusters' },
  node:                  { label: 'Node',      pluralLabel: 'nodes',      color: '#a78bfa', path: '/nodes' },
  namespace:             { label: 'Namespace', pluralLabel: 'namespaces', color: '#60a5fa', path: '/namespaces' },
  pod:                   { label: 'Pod',       pluralLabel: 'pods',       color: '#34d399', path: '/pods' },
  workload:              { label: 'Workload',  pluralLabel: 'workloads',  color: '#fbbf24', path: '/workloads' },
  service:               { label: 'Service',   pluralLabel: 'services',   color: '#f472b6', path: '/services' },
  ingress:               { label: 'Ingress',   pluralLabel: 'ingresses',  color: '#fb923c', path: '/ingresses' },
  persistentvolume:      { label: 'PV',        pluralLabel: 'PVs',        color: '#94a3b8', path: '/persistentvolumes' },
  persistentvolumeclaim: { label: 'PVC',       pluralLabel: 'PVCs',       color: '#cbd5e1', path: '/persistentvolumeclaims' },
};

const FILTER_ORDER: api.ImpactEntityType[] = [
  'cluster',
  'node',
  'namespace',
  'workload',
  'pod',
  'service',
  'ingress',
  'persistentvolume',
  'persistentvolumeclaim',
];

// At production scale a depth=2 walk from a cluster pulls in hundreds of pods
// and PVCs; default-hide them so the graph is readable, and let operators
// toggle them back on as needed.
const DEFAULT_HIDDEN: api.ImpactEntityType[] = ['pod', 'persistentvolumeclaim'];

// --- Visual graph types -----------------------------------------------------

// VisualNode is either a real entity node or a synthesized group node that
// stands in for N hidden same-type siblings of a parent.
interface VisualNode {
  id: string;
  type: api.ImpactEntityType;
  name: string;
  status?: string;
  kind?: string;
  isGroup?: boolean;
  groupKey?: string;
  groupCount?: number;
}

interface VisualEdge {
  from: string;
  to: string;
  relation: string;
}

interface VisualGraph {
  rootId: string;
  nodes: VisualNode[];
  edges: VisualEdge[];
}

function buildVisualGraph(
  graph: api.ImpactGraph,
  hiddenTypes: Set<api.ImpactEntityType>,
  expandedGroups: Set<string>,
): VisualGraph {
  const rootId = graph.root.id;

  // 1. Filter by type. Always keep the root, even if its type is hidden.
  const keep = (n: api.ImpactGraphNode) => n.id === rootId || !hiddenTypes.has(n.type);
  const fNodes = graph.nodes.filter(keep);
  const fIds = new Set(fNodes.map((n) => n.id));
  const fEdges = graph.edges.filter((e) => fIds.has(e.from) && fIds.has(e.to));

  // 2. BFS from root to assign levels (used to identify parent → child
  //    direction for grouping).
  const adj = new Map<string, Set<string>>();
  for (const e of fEdges) {
    if (!adj.has(e.from)) adj.set(e.from, new Set());
    if (!adj.has(e.to)) adj.set(e.to, new Set());
    adj.get(e.from)!.add(e.to);
    adj.get(e.to)!.add(e.from);
  }
  const level = new Map<string, number>();
  level.set(rootId, 0);
  const queue: string[] = [rootId];
  while (queue.length > 0) {
    const id = queue.shift()!;
    const lvl = level.get(id)!;
    for (const neighbor of adj.get(id) ?? []) {
      if (!level.has(neighbor)) {
        level.set(neighbor, lvl + 1);
        queue.push(neighbor);
      }
    }
  }

  // 3. Drop nodes unreachable from root after filtering.
  let visualNodes: VisualNode[] = fNodes
    .filter((n) => level.has(n.id))
    .map((n) => ({
      id: n.id,
      type: n.type,
      name: n.name,
      status: n.status,
      kind: n.kind,
    }));
  let visualEdges: VisualEdge[] = fEdges
    .filter((e) => level.has(e.from) && level.has(e.to))
    .map((e) => ({ from: e.from, to: e.to, relation: e.relation }));
  const nodesById = new Map(visualNodes.map((n) => [n.id, n]));

  // 4. Collapse same-type siblings into group nodes when over threshold.
  //    For each parent (lower level), gather neighbors one level deeper,
  //    partition by entity type, and replace each oversized partition with a
  //    single group node + edge from the parent.
  const childrenByParent = new Map<string, string[]>();
  for (const e of visualEdges) {
    const la = level.get(e.from);
    const lb = level.get(e.to);
    if (la === undefined || lb === undefined) continue;
    let parent: string, child: string;
    if (la + 1 === lb) { parent = e.from; child = e.to; }
    else if (lb + 1 === la) { parent = e.to; child = e.from; }
    else continue;
    if (!childrenByParent.has(parent)) childrenByParent.set(parent, []);
    childrenByParent.get(parent)!.push(child);
  }

  const subsumed = new Set<string>();
  const groupNodes: VisualNode[] = [];
  const groupEdges: VisualEdge[] = [];

  for (const [parent, children] of childrenByParent.entries()) {
    const byType = new Map<api.ImpactEntityType, string[]>();
    for (const c of children) {
      const node = nodesById.get(c);
      if (!node) continue;
      if (!byType.has(node.type)) byType.set(node.type, []);
      byType.get(node.type)!.push(c);
    }
    for (const [t, members] of byType.entries()) {
      const groupKey = `${parent}|${t}`;
      if (members.length > GROUP_THRESHOLD && !expandedGroups.has(groupKey)) {
        const groupId = `group:${groupKey}`;
        const meta = TYPE_META[t];
        groupNodes.push({
          id: groupId,
          type: t,
          name: `${members.length} ${meta?.pluralLabel ?? t}`,
          isGroup: true,
          groupKey,
          groupCount: members.length,
        });
        groupEdges.push({ from: parent, to: groupId, relation: 'contains' });
        for (const m of members) subsumed.add(m);
      }
    }
  }

  visualNodes = visualNodes.filter((n) => !subsumed.has(n.id));
  visualEdges = visualEdges.filter(
    (e) => !subsumed.has(e.from) && !subsumed.has(e.to),
  );
  visualNodes.push(...groupNodes);
  visualEdges.push(...groupEdges);

  // 5. Re-prune orphans (collapsing a level can leave deeper nodes adrift).
  const adj2 = new Map<string, Set<string>>();
  for (const e of visualEdges) {
    if (!adj2.has(e.from)) adj2.set(e.from, new Set());
    if (!adj2.has(e.to)) adj2.set(e.to, new Set());
    adj2.get(e.from)!.add(e.to);
    adj2.get(e.to)!.add(e.from);
  }
  const reach = new Set<string>([rootId]);
  const q2: string[] = [rootId];
  while (q2.length > 0) {
    const id = q2.shift()!;
    for (const n of adj2.get(id) ?? []) {
      if (!reach.has(n)) {
        reach.add(n);
        q2.push(n);
      }
    }
  }
  visualNodes = visualNodes.filter((n) => reach.has(n.id));
  visualEdges = visualEdges.filter(
    (e) => reach.has(e.from) && reach.has(e.to),
  );

  return { rootId, nodes: visualNodes, edges: visualEdges };
}

// --- Hierarchical layout ----------------------------------------------------

interface LayoutNode {
  id: string;
  x: number;
  y: number;
  node: VisualNode;
}

interface LayoutEdge {
  from: LayoutNode;
  to: LayoutNode;
  relation: string;
}

function layoutGraph(graph: VisualGraph): {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
} {
  const adj = new Map<string, Set<string>>();
  for (const e of graph.edges) {
    if (!adj.has(e.from)) adj.set(e.from, new Set());
    if (!adj.has(e.to)) adj.set(e.to, new Set());
    adj.get(e.from)!.add(e.to);
    adj.get(e.to)!.add(e.from);
  }

  const level = new Map<string, number>();
  const queue: string[] = [graph.rootId];
  level.set(graph.rootId, 0);
  while (queue.length > 0) {
    const id = queue.shift()!;
    const lvl = level.get(id)!;
    for (const neighbor of adj.get(id) ?? []) {
      if (!level.has(neighbor)) {
        level.set(neighbor, lvl + 1);
        queue.push(neighbor);
      }
    }
  }
  for (const n of graph.nodes) {
    if (!level.has(n.id)) level.set(n.id, 999);
  }

  const levels = new Map<number, VisualNode[]>();
  for (const n of graph.nodes) {
    const lvl = level.get(n.id) ?? 0;
    if (!levels.has(lvl)) levels.set(lvl, []);
    levels.get(lvl)!.push(n);
  }

  const sortedLevels = [...levels.keys()].sort((a, b) => a - b);
  let maxLevelWidth = 0;
  for (const lvl of sortedLevels) {
    maxLevelWidth = Math.max(maxLevelWidth, levels.get(lvl)!.length);
  }

  const nodeMap = new Map<string, LayoutNode>();
  for (const lvl of sortedLevels) {
    const group = levels.get(lvl)!;
    const totalW = group.length * NODE_W + (group.length - 1) * H_GAP;
    const maxW = maxLevelWidth * NODE_W + (maxLevelWidth - 1) * H_GAP;
    const offsetX = (maxW - totalW) / 2;
    for (let i = 0; i < group.length; i++) {
      const n = group[i];
      nodeMap.set(n.id, {
        id: n.id,
        x: offsetX + i * (NODE_W + H_GAP),
        y: lvl * (NODE_H + V_GAP),
        node: n,
      });
    }
  }

  const layoutEdges: LayoutEdge[] = [];
  for (const e of graph.edges) {
    const from = nodeMap.get(e.from);
    const to = nodeMap.get(e.to);
    if (from && to) {
      layoutEdges.push({ from, to, relation: e.relation });
    }
  }

  const allNodes = [...nodeMap.values()];
  const width = Math.max(...allNodes.map((n) => n.x + NODE_W), 400);
  const height = Math.max(...allNodes.map((n) => n.y + NODE_H), 200);

  return { nodes: allNodes, edges: layoutEdges, width, height };
}

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), hi);

// --- SVG rendering ----------------------------------------------------------

function GraphSVG({
  graph,
  rootId,
  onExpandGroup,
}: {
  graph: VisualGraph;
  rootId: string;
  onExpandGroup: (key: string) => void;
}) {
  const navigate = useNavigate();
  const layout = useMemo(() => layoutGraph(graph), [graph]);
  const pad = 40;
  const viewBox = `${-pad} ${-pad} ${layout.width + pad * 2} ${layout.height + pad * 2}`;

  const svgRef = useRef<SVGSVGElement | null>(null);
  const [transform, setTransform] = useState({ tx: 0, ty: 0, scale: 1 });
  const dragState = useRef<{
    startClientX: number;
    startClientY: number;
    startTx: number;
    startTy: number;
    moved: boolean;
  } | null>(null);
  const draggedRef = useRef(false);

  // React's onWheel is passive; preventDefault is ignored unless we attach a
  // native wheel listener with { passive: false }.
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = svg.getBoundingClientRect();
      // Mouse position in viewBox coords.
      const vbX = -pad;
      const vbY = -pad;
      const vbW = layout.width + pad * 2;
      const vbH = layout.height + pad * 2;
      const mx = vbX + ((e.clientX - rect.left) / rect.width) * vbW;
      const my = vbY + ((e.clientY - rect.top) / rect.height) * vbH;

      setTransform((prev) => {
        const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
        const nextScale = clamp(prev.scale * factor, MIN_SCALE, MAX_SCALE);
        const ratio = nextScale / prev.scale;
        return {
          scale: nextScale,
          tx: mx - (mx - prev.tx) * ratio,
          ty: my - (my - prev.ty) * ratio,
        };
      });
    };
    svg.addEventListener('wheel', onWheel, { passive: false });
    return () => svg.removeEventListener('wheel', onWheel);
  }, [layout.width, layout.height]);

  const onMouseDown = (e: ReactMouseEvent<SVGSVGElement>) => {
    if (e.button !== 0) return;
    dragState.current = {
      startClientX: e.clientX,
      startClientY: e.clientY,
      startTx: transform.tx,
      startTy: transform.ty,
      moved: false,
    };
    draggedRef.current = false;
  };

  const onMouseMove = (e: ReactMouseEvent<SVGSVGElement>) => {
    const ds = dragState.current;
    if (!ds) return;
    const dx = e.clientX - ds.startClientX;
    const dy = e.clientY - ds.startClientY;
    if (!ds.moved && Math.abs(dx) + Math.abs(dy) > DRAG_THRESHOLD_PX) {
      ds.moved = true;
      draggedRef.current = true;
    }
    if (ds.moved) {
      // Mouse delta is in screen pixels; viewBox isn't 1:1 with the rendered
      // SVG, so scale the delta into viewBox space before applying it.
      const svg = svgRef.current;
      if (!svg) return;
      const rect = svg.getBoundingClientRect();
      const vbW = layout.width + pad * 2;
      const vbH = layout.height + pad * 2;
      const scaledDx = (dx / rect.width) * vbW;
      const scaledDy = (dy / rect.height) * vbH;
      setTransform((prev) => ({ ...prev, tx: ds.startTx + scaledDx, ty: ds.startTy + scaledDy }));
    }
  };

  const endDrag = () => {
    dragState.current = null;
  };

  const reset = useCallback(() => {
    setTransform({ tx: 0, ty: 0, scale: 1 });
  }, []);

  const handleNodeClick = (ln: LayoutNode) => (e: ReactMouseEvent<SVGGElement>) => {
    if (draggedRef.current) {
      e.stopPropagation();
      return;
    }
    if (ln.node.isGroup && ln.node.groupKey) {
      onExpandGroup(ln.node.groupKey);
      return;
    }
    const meta = TYPE_META[ln.node.type];
    if (!meta) return;
    navigate(`${meta.path}/${ln.id}`);
  };

  return (
    <div className="impact-graph-container">
      <div className="impact-graph-toolbar">
        <span className="muted" style={{ fontSize: '0.78rem' }}>
          Scroll to zoom · drag to pan
        </span>
        <div className="impact-graph-zoom">
          <button
            type="button"
            className="lv-btn lv-btn-ghost lv-btn-sm"
            onClick={() =>
              setTransform((p) => ({ ...p, scale: clamp(p.scale / 1.2, MIN_SCALE, MAX_SCALE) }))
            }
            aria-label="Zoom out"
          >
            −
          </button>
          <span className="impact-graph-zoom-level">{Math.round(transform.scale * 100)}%</span>
          <button
            type="button"
            className="lv-btn lv-btn-ghost lv-btn-sm"
            onClick={() =>
              setTransform((p) => ({ ...p, scale: clamp(p.scale * 1.2, MIN_SCALE, MAX_SCALE) }))
            }
            aria-label="Zoom in"
          >
            +
          </button>
          <button
            type="button"
            className="lv-btn lv-btn-ghost lv-btn-sm"
            onClick={reset}
            aria-label="Reset view"
          >
            Reset
          </button>
        </div>
      </div>
      <svg
        ref={svgRef}
        viewBox={viewBox}
        className={`impact-graph-svg${dragState.current ? ' dragging' : ''}`}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={endDrag}
        onMouseLeave={endDrag}
      >
        <defs>
          <marker
            id="arrowhead"
            markerWidth="8"
            markerHeight="6"
            refX="8"
            refY="3"
            orient="auto"
          >
            <polygon points="0 0, 8 3, 0 6" fill="#555" />
          </marker>
        </defs>
        <g transform={`translate(${transform.tx} ${transform.ty}) scale(${transform.scale})`}>
          {layout.edges.map((e, i) => {
            const x1 = e.from.x + NODE_W / 2;
            const y1 = e.from.y + NODE_H;
            const x2 = e.to.x + NODE_W / 2;
            const y2 = e.to.y;
            const midY = (y1 + y2) / 2;
            return (
              <g key={i}>
                <path
                  d={`M ${x1} ${y1} C ${x1} ${midY}, ${x2} ${midY}, ${x2} ${y2}`}
                  fill="none"
                  stroke="#444"
                  strokeWidth="1.5"
                  markerEnd="url(#arrowhead)"
                />
                <text
                  x={(x1 + x2) / 2}
                  y={midY - 4}
                  textAnchor="middle"
                  className="impact-edge-label"
                >
                  {e.relation}
                </text>
              </g>
            );
          })}
          {layout.nodes.map((ln) => {
            const meta = TYPE_META[ln.node.type] || {
              label: ln.node.type,
              pluralLabel: ln.node.type,
              color: '#888',
              path: '',
            };
            const isRoot = ln.id === rootId;
            const isGroup = ln.node.isGroup === true;
            return (
              <g
                key={ln.id}
                className="impact-node-link"
                onClick={handleNodeClick(ln)}
                role="button"
                tabIndex={0}
              >
                <rect
                  x={ln.x}
                  y={ln.y}
                  width={NODE_W}
                  height={NODE_H}
                  rx="6"
                  fill={isGroup ? '#181c25' : '#1a1d21'}
                  stroke={isRoot ? meta.color : isGroup ? meta.color : '#333'}
                  strokeWidth={isRoot ? 2.5 : 1.5}
                  strokeDasharray={isGroup ? '5 3' : undefined}
                />
                <text
                  x={ln.x + 8}
                  y={ln.y + 17}
                  className="impact-node-type"
                  fill={meta.color}
                >
                  {isGroup ? `${meta.pluralLabel.toUpperCase()} (CLICK TO EXPAND)` : meta.label}
                  {!isGroup && ln.node.kind ? ` (${ln.node.kind})` : ''}
                </text>
                <text x={ln.x + 8} y={ln.y + 34} className="impact-node-name">
                  {isGroup
                    ? `+${ln.node.groupCount}`
                    : ln.node.name.length > 20
                    ? ln.node.name.slice(0, 18) + '\u2026'
                    : ln.node.name}
                </text>
                {!isGroup && ln.node.status && (
                  <text
                    x={ln.x + NODE_W - 8}
                    y={ln.y + 34}
                    textAnchor="end"
                    className="impact-node-status"
                  >
                    {ln.node.status}
                  </text>
                )}
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
}

// --- Type filter chip bar ---------------------------------------------------

function TypeFilterBar({
  hidden,
  onToggle,
  counts,
}: {
  hidden: Set<api.ImpactEntityType>;
  onToggle: (t: api.ImpactEntityType) => void;
  counts: Map<api.ImpactEntityType, number>;
}) {
  return (
    <div className="impact-filter-bar" role="group" aria-label="Filter entity types">
      {FILTER_ORDER.map((t) => {
        const meta = TYPE_META[t];
        const n = counts.get(t) ?? 0;
        if (n === 0) return null;
        const isHidden = hidden.has(t);
        return (
          <button
            key={t}
            type="button"
            onClick={() => onToggle(t)}
            className={`impact-filter-chip${isHidden ? ' hidden' : ''}`}
            aria-pressed={!isHidden}
            style={{ ['--chip-accent' as never]: meta.color }}
            title={isHidden ? `Show ${meta.pluralLabel}` : `Hide ${meta.pluralLabel}`}
          >
            <span className="impact-filter-dot" />
            {meta.pluralLabel} ({n})
          </button>
        );
      })}
    </div>
  );
}

// --- Public component -------------------------------------------------------

const ENTITY_TYPE_FOR_DETAIL: Record<string, api.ImpactEntityType> = {
  clusters: 'cluster',
  nodes: 'node',
  namespaces: 'namespace',
  pods: 'pod',
  workloads: 'workload',
  services: 'service',
  ingresses: 'ingress',
  persistentvolumes: 'persistentvolume',
  persistentvolumeclaims: 'persistentvolumeclaim',
};

export function ImpactSection({
  entityType,
  entityId,
}: {
  entityType: string;
  entityId: string;
}) {
  const impactType = ENTITY_TYPE_FOR_DETAIL[entityType];
  const [depth, setDepth] = useState(2);
  const [hidden, setHidden] = useState<Set<api.ImpactEntityType>>(
    () => new Set(DEFAULT_HIDDEN),
  );
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(() => new Set());

  const state = useResource(
    () => api.getImpactGraph(impactType, entityId, depth),
    [entityId, depth],
  );

  const toggleType = useCallback((t: api.ImpactEntityType) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });
    // Changing filters invalidates the previous group choices.
    setExpandedGroups(new Set());
  }, []);

  const expandGroup = useCallback((key: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      next.add(key);
      return next;
    });
  }, []);

  const collapseAll = useCallback(() => setExpandedGroups(new Set()), []);

  if (!impactType) return null;

  return (
    <div className="lv-card">
      <div className="lv-card-header">
        <h3 className="lv-card-title">Impact graph</h3>
        <Tabs
          items={[
            { key: '1', label: '1 hop' },
            { key: '2', label: '2 hops' },
            { key: '3', label: '3 hops' },
          ]}
          active={String(depth)}
          onChange={(k) => setDepth(Number(k))}
        />
      </div>
      <AsyncView state={state}>
        {(graph) => {
          const counts = new Map<api.ImpactEntityType, number>();
          for (const n of graph.nodes) {
            counts.set(n.type, (counts.get(n.type) ?? 0) + 1);
          }
          const visual = buildVisualGraph(graph, hidden, expandedGroups);
          const collapsedCount = visual.nodes.filter((n) => n.isGroup).length;
          return (
            <>
              <TypeFilterBar hidden={hidden} onToggle={toggleType} counts={counts} />
              <p className="muted" style={{ fontSize: '0.85rem', margin: '0 0 0.5rem' }}>
                Showing {visual.nodes.length} of {graph.nodes.length} components
                {collapsedCount > 0 && (
                  <>
                    {' · '}
                    {collapsedCount} group{collapsedCount === 1 ? '' : 's'} collapsed
                    {' '}
                    <button
                      type="button"
                      className="link-btn"
                      onClick={collapseAll}
                      style={{ marginLeft: '0.25rem' }}
                    >
                      collapse all
                    </button>
                  </>
                )}
                {' · '}
                {visual.edges.length} relationship{visual.edges.length === 1 ? '' : 's'}.
              </p>
              <GraphSVG graph={visual} rootId={visual.rootId} onExpandGroup={expandGroup} />
            </>
          );
        }}
      </AsyncView>
    </div>
  );
}
