import type { ContainerVersionInfo } from '../api';

// extractTag returns the tag substring of `repo:tag` or empty when the
// image is digest-only or untagged. Local-registry refs may use ':' in
// the port segment of the host; we mirror the Go-side splitRef logic and
// only consider a colon after the last '/'.
function extractTag(image: string): string {
  const slash = image.lastIndexOf('/');
  const tail = slash >= 0 ? image.slice(slash + 1) : image;
  const colon = tail.lastIndexOf(':');
  if (colon < 0) return '';
  return tail.slice(colon + 1);
}

type Props = {
  /** The pod's actual image reference (e.g. local-registry/.../foo:0.26). */
  image: string;
  /** ContainerVersionInfo for this container, or undefined if unknown. */
  info?: ContainerVersionInfo | null;
};

// OriginLine renders a muted "origin: …" sub-line below a container's
// image ref on Pod and Workload detail tables. Three branches:
//   - resolved   → "origin: <origin_image_repo>:<tag>"
//   - unresolved → muted "origin: unknown" with origin_error on hover
//   - absent     → renders nothing (passthrough image, no origin step)
export function OriginLine({ image, info }: Props) {
  if (!info?.origin_status) return null;
  if (info.origin_status === 'resolved' && info.origin_image_repo) {
    const tag = extractTag(image);
    const refWithTag = tag ? `${info.origin_image_repo}:${tag}` : info.origin_image_repo;
    return (
      <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        origin: <code>{refWithTag}</code>
      </div>
    );
  }
  // unresolved
  return (
    <div className="muted" style={{ fontSize: 'var(--fs-sm)' }} title={info.origin_error ?? ''}>
      origin: unknown
    </div>
  );
}
