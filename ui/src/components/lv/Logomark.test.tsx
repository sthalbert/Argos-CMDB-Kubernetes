import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Logomark, LogomarkLarge } from './Logomark';

describe('Logomark', () => {
  it('renders an SVG with the requested size', () => {
    const { container } = render(<Logomark size={32} />);
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('width')).toBe('32');
    expect(svg.getAttribute('height')).toBe('32');
    expect(svg.getAttribute('aria-hidden')).toBe('true');
  });

  it('renders 7 spokes', () => {
    const { container } = render(<Logomark size={28} />);
    const lines = container.querySelectorAll('svg line');
    expect(lines.length).toBe(7);
  });
});

describe('LogomarkLarge', () => {
  it('renders cross-hairs for the lens detail', () => {
    const { container } = render(<LogomarkLarge size={180} />);
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('viewBox')).toBe('0 0 180 180');
    // Cross-hair lines = 2 (horizontal + vertical) plus the 7 spokes.
    const lines = container.querySelectorAll('svg line');
    expect(lines.length).toBe(9);
  });
});
