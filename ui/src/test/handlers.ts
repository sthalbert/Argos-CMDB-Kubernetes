import { http, HttpResponse } from 'msw';
import {
  fixtureAuditEvent, fixtureAuthConfig, fixtureCloudAccount, fixtureCluster,
  fixtureImpactGraph, fixtureIngress, fixtureMe, fixtureNamespace,
  fixtureNode, fixturePV, fixturePVC, fixturePod, fixtureService,
  fixtureSession, fixtureSettings, fixtureToken, fixtureUser,
  fixtureVirtualMachine, fixtureWorkload, paged,
} from './fixtures';

// Default handler set — every endpoint api.ts can call has at least one
// happy-path entry. Tests override per case via server.use().
export const handlers = [
  // --- auth ---
  http.get('/v1/auth/me', () => HttpResponse.json(fixtureMe)),
  http.get('/v1/auth/config', () => HttpResponse.json(fixtureAuthConfig)),
  http.post('/v1/auth/login', () => new HttpResponse(null, { status: 204 })),
  http.post('/v1/auth/logout', () => new HttpResponse(null, { status: 204 })),
  http.post('/v1/auth/change-password', () => new HttpResponse(null, { status: 204 })),

  // --- admin: users / tokens / sessions / audit / settings / cloud accounts ---
  http.get('/v1/admin/users', () => HttpResponse.json(paged([fixtureUser]))),
  http.post('/v1/admin/users', () => HttpResponse.json(fixtureUser)),
  http.patch('/v1/admin/users/:id', () => HttpResponse.json(fixtureUser)),
  http.delete('/v1/admin/users/:id', () => new HttpResponse(null, { status: 204 })),

  http.get('/v1/admin/tokens', () => HttpResponse.json(paged([fixtureToken]))),
  http.post('/v1/admin/tokens', () =>
    HttpResponse.json({ ...fixtureToken, token: 'argos_pat_abcd1234_xxx' })),
  http.delete('/v1/admin/tokens/:id', () => new HttpResponse(null, { status: 204 })),

  http.get('/v1/admin/sessions', () => HttpResponse.json(paged([fixtureSession]))),
  http.delete('/v1/admin/sessions/:id', () => new HttpResponse(null, { status: 204 })),

  http.get('/v1/admin/audit', () => HttpResponse.json(paged([fixtureAuditEvent]))),

  http.get('/v1/admin/settings', () => HttpResponse.json(fixtureSettings)),
  http.patch('/v1/admin/settings', () => HttpResponse.json(fixtureSettings)),

  http.get('/v1/admin/cloud-accounts', () => HttpResponse.json(paged([fixtureCloudAccount]))),
  http.get('/v1/admin/cloud-accounts/:id', () => HttpResponse.json(fixtureCloudAccount)),
  http.post('/v1/admin/cloud-accounts', () => HttpResponse.json(fixtureCloudAccount)),
  http.patch('/v1/admin/cloud-accounts/:id', () => HttpResponse.json(fixtureCloudAccount)),
  http.patch('/v1/admin/cloud-accounts/:id/credentials', () => HttpResponse.json(fixtureCloudAccount)),
  http.post('/v1/admin/cloud-accounts/:id/disable', () => new HttpResponse(null, { status: 204 })),
  http.post('/v1/admin/cloud-accounts/:id/enable', () => new HttpResponse(null, { status: 204 })),
  http.delete('/v1/admin/cloud-accounts/:id', () => new HttpResponse(null, { status: 204 })),
  http.post('/v1/admin/cloud-accounts/:id/tokens', () =>
    HttpResponse.json({ ...fixtureToken, bound_cloud_account_id: fixtureCloudAccount.id, token: 'argos_pat_abcd1234_yyy' })),

  // --- CMDB lists ---
  http.get('/v1/clusters', () => HttpResponse.json(paged([fixtureCluster]))),
  http.get('/v1/clusters/:id', () => HttpResponse.json(fixtureCluster)),
  http.patch('/v1/clusters/:id', () => HttpResponse.json(fixtureCluster)),
  http.delete('/v1/clusters/:id', () => new HttpResponse(null, { status: 204 })),

  http.get('/v1/namespaces', () => HttpResponse.json(paged([fixtureNamespace]))),
  http.get('/v1/namespaces/:id', () => HttpResponse.json(fixtureNamespace)),
  http.patch('/v1/namespaces/:id', () => HttpResponse.json(fixtureNamespace)),

  http.get('/v1/nodes', () => HttpResponse.json(paged([fixtureNode]))),
  http.get('/v1/nodes/:id', () => HttpResponse.json(fixtureNode)),
  http.patch('/v1/nodes/:id', () => HttpResponse.json(fixtureNode)),

  http.get('/v1/workloads', () => HttpResponse.json(paged([fixtureWorkload]))),
  http.get('/v1/workloads/:id', () => HttpResponse.json(fixtureWorkload)),

  http.get('/v1/pods', () => HttpResponse.json(paged([fixturePod]))),
  http.get('/v1/pods/:id', () => HttpResponse.json(fixturePod)),

  http.get('/v1/services', () => HttpResponse.json(paged([fixtureService]))),
  http.get('/v1/services/:id', () => HttpResponse.json(fixtureService)),

  http.get('/v1/ingresses', () => HttpResponse.json(paged([fixtureIngress]))),
  http.get('/v1/ingresses/:id', () => HttpResponse.json(fixtureIngress)),

  http.get('/v1/persistentvolumes', () => HttpResponse.json(paged([fixturePV]))),
  http.get('/v1/persistentvolumes/:id', () => HttpResponse.json(fixturePV)),

  http.get('/v1/persistentvolumeclaims', () => HttpResponse.json(paged([fixturePVC]))),
  http.get('/v1/persistentvolumeclaims/:id', () => HttpResponse.json(fixturePVC)),

  http.get('/v1/impact/:type/:id', () => HttpResponse.json(fixtureImpactGraph)),

  // --- virtual machines ---
  http.get('/v1/virtual-machines/applications/distinct', () =>
    HttpResponse.json({ products: [{ product: 'nginx', versions: ['1.27.0'] }] })),
  http.get('/v1/virtual-machines', () => HttpResponse.json(paged([fixtureVirtualMachine]))),
  http.get('/v1/virtual-machines/:id', () => HttpResponse.json(fixtureVirtualMachine)),
  http.patch('/v1/virtual-machines/:id', () => HttpResponse.json(fixtureVirtualMachine)),
  http.delete('/v1/virtual-machines/:id', () => new HttpResponse(null, { status: 204 })),

  // --- health ---
  http.get('/healthz', () => HttpResponse.json({ status: 'ok', version: 'test' })),
];
