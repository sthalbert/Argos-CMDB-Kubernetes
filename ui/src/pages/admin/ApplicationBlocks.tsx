// Application blocks admin page placeholder. The real CRUD page (create
// block, edit, delete, list applications inside the block) lands in
// ADR-0029 Phase 3 bundle B. Blocks are configured under /admin/ so the
// CRUD lives next to other portfolio-level admin actions.

export function ApplicationBlocks() {
  return (
    <div>
      <h3>Application blocks</h3>
      <p className="muted">
        Application blocks admin page (coming in Phase 3 bundle B).
      </p>
    </div>
  );
}

export default ApplicationBlocks;
