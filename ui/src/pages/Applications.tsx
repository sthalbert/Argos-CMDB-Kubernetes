// Applications list page placeholder. The real list (search, DICT
// filter, criticality + block filters) lands in ADR-0029 Phase 3
// bundle B. We register the route now so deep links from
// ApplicationCard don't 404 against the SPA shell.

export function Applications() {
  return (
    <div>
      <h2>Applications</h2>
      <p className="muted">Applications list page (coming in Phase 3 bundle B).</p>
    </div>
  );
}

export default Applications;
