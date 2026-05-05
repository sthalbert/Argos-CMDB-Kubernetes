import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView } from '../components';

// --- Layout constants -------------------------------------------------------

const NODE_W = 180;
const NODE_H = 50;
const H_GAP = 24;
const V_GAP = 70;

// --- Entity type metadata ---------------------------------------------------

const TYPE_META: Record<
  api.ImpactEntityType,
  { label: string; color: string; path: string }
> = {
  cluster: { label: 'Cluster', color: '#5cc8ff', path: '/clusters' },
  node: { label: 'Node', color: '#a78bfa', path: '/nodes' },
  namespace: { label: 'Namespace', color: '#60a5fa', path: '/namespaces' },
  pod: { label: 'Pod', color: '#34d399', path: '/pods' },
  workload: { label: 'Workload', color: '#fbbf24', path: '/workloads' },
  service: { label: 'Service', color: '#f472b6', path: '/services' },
  ingress: { label: 'Ingress', color: '#fb923c', path: '/ingresses' },
  persistentvolume: { label: 'PV', color: '#94a3b8', path: '/persistentvolumes' },
  persistentvolumeclaim: { label: 'PVC', color: '#cbd5e1', path: '/persistentvolumeclaims' },
};

// --- Hierarchical layout ----------------------------------------------------

// Assign depth levels by BFS from root, then position nodes.
interface LayoutNode {
  id: string;
  x: number;
  y: number;
  node: api.ImpactGraphNode;
}

interface LayoutEdge {
  from: LayoutNode;
  to: LayoutNode;
  relation: string;
}

function layoutGraph(graph: api.ImpactGraph): {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
} {
  // Build adjacency (undirected for BFS).
  const adj = new Map<string, Set<string>>();
  for (const e of graph.edges) {
    if (!adj.has(e.from)) adj.set(e.from, new Set());
    if (!adj.has(e.to)) adj.set(e.to, new Set());
    adj.get(e.from)!.add(e.to);
    adj.get(e.to)!.add(e.from);
  }

  // BFS from root to assign levels.
  const level = new Map<string, number>();
  const queue: string[] = [graph.root.id];
  level.set(graph.root.id, 0);
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
  // Nodes not reachable from root (shouldn't happen but guard).
  for (const n of graph.nodes) {
    if (!level.has(n.id)) level.set(n.id, 999);
  }

  // Group by level.
  const levels = new Map<number, api.ImpactGraphNode[]>();
  for (const n of graph.nodes) {
    const lvl = level.get(n.id) ?? 0;
    if (!levels.has(lvl)) levels.set(lvl, []);
    levels.get(lvl)!.push(n);
  }

  // Position nodes.
  const nodeMap = new Map<string, LayoutNode>();
  const sortedLevels = [...levels.keys()].sort((a, b) => a - b);
  let maxLevelWidth = 0;
  for (const lvl of sortedLevels) {
    const group = levels.get(lvl)!;
    maxLevelWidth = Math.max(maxLevelWidth, group.length);
  }

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

  // Build layout edges.
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

// --- SVG rendering ----------------------------------------------------------

function GraphSVG({
  graph,
  rootId,
}: {
  graph: api.ImpactGraph;
  rootId: string;
}) {
  const layout = useMemo(() => layoutGraph(graph), [graph]);
  const pad = 40;
  const baseW = layout.width + pad * 2;
  const baseH = layout.height + pad * 2;

  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const panRef = useRef({ active: false, startX: 0, startY: 0, origX: 0, origY: 0 });
  const containerRef = useRef<HTMLDivElement | null>(null);

  const onWheel = useCallback((e: React.WheelEvent) => {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
    setZoom((z) => Math.max(0.05, Math.min(50, z * factor)));
  }, []);

  const onMouseDown = useCallback((e: React.MouseEvent) => {
    panRef.current = { active: true, startX: e.clientX, startY: e.clientY, origX: pan.x, origY: pan.y };
  }, [pan]);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!panRef.current.active) return;
      const dx = (e.clientX - panRef.current.startX) / zoom;
      const dy = (e.clientY - panRef.current.startY) / zoom;
      setPan({ x: panRef.current.origX - dx, y: panRef.current.origY - dy });
    };
    const onUp = () => { panRef.current.active = false; };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
  }, [zoom]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const handler = (e: WheelEvent) => e.preventDefault();
    el.addEventListener('wheel', handler, { passive: false });
    return () => el.removeEventListener('wheel', handler);
  }, []);

  const vbW = baseW / zoom;
  const vbH = baseH / zoom;
  const vbX = -pad + pan.x;
  const vbY = -pad + pan.y;

  return (
    <div className="impact-graph-container" ref={containerRef} onWheel={onWheel} onMouseDown={onMouseDown} style={{ position: 'relative', cursor: 'grab' }}>
      <div style={{ position: 'absolute', top: 8, right: 8, display: 'flex', gap: 4, zIndex: 2 }}>
        <button className="depth-btn" onClick={(e) => { e.stopPropagation(); setZoom((z) => Math.min(50, z * 1.25)); }}>+</button>
        <button className="depth-btn" onClick={(e) => { e.stopPropagation(); setZoom((z) => Math.max(0.05, z / 1.25)); }}>−</button>
        <button className="depth-btn" onClick={(e) => { e.stopPropagation(); setZoom(1); setPan({ x: 0, y: 0 }); }}>reset</button>
        <span className="muted" style={{ fontSize: '0.75rem', alignSelf: 'center', marginLeft: 4 }}>{Math.round(zoom * 100)}%</span>
      </div>
      <svg
        viewBox={`${vbX} ${vbY} ${vbW} ${vbH}`}
        className="impact-graph-svg"
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
            color: '#888',
            path: '',
          };
          const isRoot = ln.id === rootId;
          return (
            <Link key={ln.id} to={`${meta.path}/${ln.id}`}>
              <g>
                <rect
                  x={ln.x}
                  y={ln.y}
                  width={NODE_W}
                  height={NODE_H}
                  rx="6"
                  fill="#1a1d21"
                  stroke={isRoot ? meta.color : '#333'}
                  strokeWidth={isRoot ? 2.5 : 1}
                />
                <text
                  x={ln.x + 8}
                  y={ln.y + 17}
                  className="impact-node-type"
                  fill={meta.color}
                >
                  {meta.label}
                  {ln.node.kind ? ` (${ln.node.kind})` : ''}
                </text>
                <text x={ln.x + 8} y={ln.y + 34} className="impact-node-name">
                  {ln.node.name.length > 20
                    ? ln.node.name.slice(0, 18) + '\u2026'
                    : ln.node.name}
                </text>
                {ln.node.status && (
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
            </Link>
          );
        })}
      </svg>
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

  const state = useResource(
    () => api.getImpactGraph(impactType, entityId, depth),
    [entityId, depth],
  );

  const handleDepth = useCallback(
    (d: number) => setDepth(d),
    [],
  );

  if (!impactType) return null;

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', margin: '1rem 0 0.5rem' }}>
        <h3 style={{ margin: 0 }}>Impact graph</h3>
        <span className="muted" style={{ fontSize: '0.8rem' }}>Depth:</span>
        {[1, 2, 3].map((d) => (
          <button
            key={d}
            className={`depth-btn${d === depth ? ' depth-active' : ''}`}
            onClick={() => handleDepth(d)}
          >
            {d}
          </button>
        ))}
      </div>
      <AsyncView state={state}>
        {(graph) => (
          <>
            <p className="muted" style={{ fontSize: '0.85rem', margin: '0 0 0.5rem' }}>
              {graph.nodes.length} component{graph.nodes.length === 1 ? '' : 's'},{' '}
              {graph.edges.length} relationship{graph.edges.length === 1 ? '' : 's'}.
              Click a node to navigate.
            </p>
            <GraphSVG graph={graph} rootId={graph.root.id} />
          </>
        )}
      </AsyncView>
    </>
  );
}
