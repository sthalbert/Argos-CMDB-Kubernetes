import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ExtractButton } from './ExtractButton';

describe('ExtractButton', () => {
  it('renders disabled when prop says so', () => {
    render(
      <ExtractButton label="Extract" disabled onExtract={() => Promise.resolve({ truncated: false, filename: null })} />,
    );
    expect(screen.getByRole('button', { name: /extract/i })).toBeDisabled();
  });

  it('opens a menu with CSV and JSON when clicked', () => {
    render(
      <ExtractButton label="Extract" onExtract={() => Promise.resolve({ truncated: false, filename: null })} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /extract/i }));
    expect(screen.getByRole('menuitem', { name: /csv/i })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /json/i })).toBeInTheDocument();
  });

  it('invokes onExtract with the chosen format', async () => {
    const onExtract = vi.fn().mockResolvedValue({ truncated: false, filename: null });
    render(<ExtractButton label="Extract" onExtract={onExtract} />);
    fireEvent.click(screen.getByRole('button', { name: /extract/i }));
    fireEvent.click(screen.getByRole('menuitem', { name: /csv/i }));
    expect(onExtract).toHaveBeenCalledWith('csv');
  });
});
