import React from 'react';

// ── LogoIcon ──────────────────────────────────────────────────────────────
// Double-hexagone : hexagone extérieur (contour) + hexagone intérieur (plein,
// tourné 30°) + centre sombre + point lumineux.
// Adapté au thème via CSS vars — fonctionne sur fond sombre comme clair.

export function LogoIcon({
  size = 32,
  className,
  style,
}: {
  size?: number;
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 96 96"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      style={style}
      aria-label="longue-vue"
      role="img"
    >
      {/* Halo doux */}
      <circle cx="48" cy="48" r="46" fill="var(--accent, #38BDF8)" fillOpacity="0.10" />

      {/* Hexagone extérieur — flat-top, contour seulement */}
      <polygon
        points="91,48 70,85 26,85 5,48 26,11 70,11"
        fill="none"
        stroke="var(--accent, #38BDF8)"
        strokeWidth="2.5"
        strokeLinejoin="round"
      />

      {/* Hexagone intérieur — pointy-top (tourné 30°), plein */}
      <polygon
        points="48,25 68,37 68,60 48,71 28,60 28,37"
        fill="var(--accent, #38BDF8)"
        fillOpacity="0.85"
      />

      {/* Centre sombre (ouverture / lentille) */}
      <circle cx="48" cy="48" r="11" fill="var(--bg-0, #0e1012)" />

      {/* Reflet interne */}
      <circle cx="42" cy="42" r="3" fill="white" fillOpacity="0.20" />

      {/* Point central lumineux */}
      <circle cx="48" cy="48" r="4.5" fill="var(--accent, #38BDF8)" fillOpacity="0.95" />
    </svg>
  );
}

// ── Logo ──────────────────────────────────────────────────────────────────
// Wordmark complet : icône + "longue" (ultra-fin, espacé) + "-vue" (gras, accent).
// Passe en icon-only quand iconOnly=true (sidebar réduite).

interface LogoProps {
  /** Affiche uniquement l'icône (sidebar réduite) */
  iconOnly?: boolean;
  /** Taille de l'icône en px */
  size?: number;
  className?: string;
  style?: React.CSSProperties;
}

export function Logo({ iconOnly = false, size = 24, className, style }: LogoProps) {
  if (iconOnly) {
    return <LogoIcon size={size} className={className} style={style} />;
  }

  return (
    <div
      className={className}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        userSelect: 'none',
        ...style,
      }}
    >
      <LogoIcon size={size} />
      <span style={{ lineHeight: 1, letterSpacing: '-0.01em' }}>
        <span
          style={{
            fontFamily: 'var(--font-sans)',
            fontSize: '0.92rem',
            fontWeight: 200,
            color: 'var(--fg-muted)',
            letterSpacing: '0.06em',
          }}
        >
          longue
        </span>
        <span
          style={{
            fontFamily: 'var(--font-sans)',
            fontSize: '0.92rem',
            fontWeight: 800,
            color: 'var(--accent)',
            letterSpacing: '-0.03em',
          }}
        >
          -vue
        </span>
      </span>
    </div>
  );
}
