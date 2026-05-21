// Package mirrorresolve resolves mirrored container image references
// (Harbor with replication) back to their public origin by reading OCI
// manifest annotations preserved during replication. Used by the
// image-versions enricher (see ADR-0022, ADR-0026).
package mirrorresolve
