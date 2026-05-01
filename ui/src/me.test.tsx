import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { canEdit, isAdmin, MeProvider, useMe } from './me';
import type { Me } from './api';

function ProbeMe() {
  const me = useMe();
  return <span data-testid="role">{me?.role ?? 'none'}</span>;
}

const baseMe: Me = { kind: 'user', scopes: [], role: 'viewer' };

describe('useMe / MeProvider', () => {
  it('returns null when no provider is mounted', () => {
    const { getByTestId } = render(<ProbeMe />);
    expect(getByTestId('role').textContent).toBe('none');
  });

  it('returns the provided value', () => {
    const { getByTestId } = render(
      <MeProvider value={{ ...baseMe, role: 'editor' }}>
        <ProbeMe />
      </MeProvider>,
    );
    expect(getByTestId('role').textContent).toBe('editor');
  });
});

describe('canEdit', () => {
  it.each([
    ['admin', true],
    ['editor', true],
    ['auditor', false],
    ['viewer', false],
  ] as const)('returns %s for role %s', (role, expected) => {
    expect(canEdit({ ...baseMe, role: role as Me['role'] })).toBe(expected);
  });

  it('returns false for null', () => {
    expect(canEdit(null)).toBe(false);
  });
});

describe('isAdmin', () => {
  it('returns true only for admin', () => {
    expect(isAdmin({ ...baseMe, role: 'admin' })).toBe(true);
    expect(isAdmin({ ...baseMe, role: 'editor' })).toBe(false);
    expect(isAdmin(null)).toBe(false);
  });
});
