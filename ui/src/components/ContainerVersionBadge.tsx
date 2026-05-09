import type { ContainerVersionInfo } from '../api'

type Props = {
  /** ContainerVersionInfo for this container, or undefined if unknown. */
  info?: ContainerVersionInfo
  /** Last error string from the registry call, if any. */
  lastError?: string | null
}

function relTime(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  const diffH = Math.round((Date.now() - t) / 3_600_000)
  if (diffH < 1) return 'less than 1h ago'
  if (diffH < 24) return `${diffH}h ago`
  return `${Math.round(diffH / 24)}d ago`
}

export function ContainerVersionBadge({ info, lastError }: Props) {
  if (lastError) {
    return (
      <span className="badge badge-error" title={`Error: ${lastError}`}>
        ⛔ error
      </span>
    )
  }
  if (!info) {
    return (
      <span className="badge badge-unknown" title="Latest version unknown for this image">
        ⚠ unknown
      </span>
    )
  }
  if (info.is_behind) {
    return (
      <span
        className="badge badge-behind"
        title={`Latest available: ${info.latest_tag} (checked ${relTime(info.last_checked_at)})`}
      >
        ↑ behind
      </span>
    )
  }
  return (
    <span
      className="badge badge-ok"
      title={`Up to date with ${info.latest_tag} (checked ${relTime(info.last_checked_at)})`}
    >
      ✓ up-to-date
    </span>
  )
}
