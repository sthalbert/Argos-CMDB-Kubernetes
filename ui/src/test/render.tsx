import { render, type RenderOptions, type RenderResult } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { type ReactElement } from 'react';

interface RenderWithRouterOptions extends RenderOptions {
  initialPath?: string;
  routePath?: string;
}

// renderWithRouter mounts `el` at `routePath` (default '*' so any path
// matches) inside a MemoryRouter starting at `initialPath` (default '/').
// Pages that call useParams need the route param shape — pass routePath
// like '/clusters/:id' and initialPath like '/clusters/abc'.
export function renderWithRouter(
  el: ReactElement,
  { initialPath = '/', routePath = '*', ...rest }: RenderWithRouterOptions = {},
): RenderResult {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path={routePath} element={el} />
      </Routes>
    </MemoryRouter>,
    rest,
  );
}
