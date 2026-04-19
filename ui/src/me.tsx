import { createContext, useContext } from 'react';
import type { Me } from './api';

// MeContext carries the authenticated user's identity + role through the
// tree so detail pages can gate edit affordances without re-fetching
// /v1/auth/me. Provider is set once inside App.tsx once auth.status
// reaches 'ready'.

const MeContext = createContext<Me | null>(null);

export const MeProvider = MeContext.Provider;

export function useMe(): Me | null {
  return useContext(MeContext);
}

// canEdit answers "can this role invoke write endpoints?". The API
// enforces scope anyway; this is just UX (hide the Edit button when
// the user can't save).
export function canEdit(me: Me | null): boolean {
  return me?.role === 'admin' || me?.role === 'editor';
}
