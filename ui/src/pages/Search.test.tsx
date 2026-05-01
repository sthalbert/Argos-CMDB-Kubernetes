import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import ImageSearch from './Search';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

describe('ImageSearch', () => {
  it('renders the search input', () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image' });
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders the empty state when no query is set', () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image' });
    expect(screen.getByText(/enter an image to search/i)).toBeInTheDocument();
  });

  it('renders results when a query is present', async () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => {
      // The callout summarising match counts always renders after the fetches settle.
      expect(screen.getByText(/matches for/i)).toBeInTheDocument();
    });
  });

  it('shows an error state when fetch calls fail', async () => {
    server.use(
      http.get('/v1/workloads', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/pods', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/virtual-machines', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/namespaces', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/clusters', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/admin/cloud-accounts', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => {
      expect(screen.getByText(/error|failed/i)).toBeInTheDocument();
    });
  });
});
