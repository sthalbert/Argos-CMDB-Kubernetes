import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { AnnotationsCard } from './AnnotationsCard';

describe('AnnotationsCard', () => {
  it('renders without crashing with no annotations', () => {
    const { container } = render(<AnnotationsCard />);
    expect(container.firstChild).not.toBeNull();
  });

  it('renders the "Annotations" section title', () => {
    const { getByText } = render(<AnnotationsCard />);
    expect(getByText('Annotations')).toBeInTheDocument();
  });

  it('renders a dash when annotations is null', () => {
    const { container } = render(<AnnotationsCard annotations={null} />);
    expect(container.textContent).toContain('—');
  });

  it('renders a dash when annotations is an empty object', () => {
    const { container } = render(<AnnotationsCard annotations={{}} />);
    expect(container.textContent).toContain('—');
  });

  it('renders chips for populated annotations', () => {
    const { container } = render(
      <AnnotationsCard annotations={{ 'longue-vue.io/env': 'prod', 'longue-vue.io/tier': 'web' }} />,
    );
    expect(container.querySelectorAll('.label-chip').length).toBe(2);
  });
});
