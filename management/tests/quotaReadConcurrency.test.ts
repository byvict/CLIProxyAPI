import { afterAll, afterEach, expect, spyOn, test } from 'bun:test';
import i18n from '@/i18n';
import { apiClient } from '@/services/api/client';
import { useQuotaStore, captureQuotaCacheGeneration, commitIfQuotaCacheCurrent } from '@/stores/useQuotaStore';
import { withQuotaHistory } from '@/features/quotaHistory/historyReader';
import type { QuotaAdapter } from '@/features/quota/providers';

const events = new EventTarget();
const previousWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
Object.defineProperty(globalThis, 'window', { configurable: true, value: events });
const file = { name: 'fixture.json', authIndex: ' test-index ', type: 'codex' };
const success = { status: 'success' as const, windows: [{ id: 'weekly', usedPercent: 50, periodHours: 168, resetAtMs: Date.now() + 86400000 }] };
function deferred() {
  let resolve: (value: unknown) => void = () => {};
  const promise = new Promise<unknown>((done) => { resolve = done; });
  return { promise, resolve };
}
const mocks: Array<{ mockRestore(): void }> = [];
afterEach(() => { mocks.splice(0).forEach((mock) => mock.mockRestore()); useQuotaStore.getState().clearQuotaCache(); });
function setup(resetResult: Promise<unknown> = Promise.resolve(success)) {
  const read = deferred();
  let reads = 0, resets = 0;
  const post = spyOn(apiClient, 'post').mockResolvedValue({});
  const capture = spyOn(apiClient, 'captureConnection').mockReturnValue(apiClient);
  mocks.push(post, capture);
  const adapter = withQuotaHistory({
    type: 'codex', i18nPrefix: 'codex_quota', filterFn: () => true,
    fetchQuota: async () => { reads++; return read.promise; },
    resetQuota: async () => { resets++; return resetResult; },
    storeSelector: (state) => state.codexQuota, storeSetter: 'setCodexQuota',
    buildLoadingState: () => ({ status: 'loading' }),
    buildErrorState: (error) => ({ status: 'error', error }),
    buildSuccessState: () => success,
    Body: () => null,
  } satisfies QuotaAdapter);
  return { adapter, read, post, reads: () => reads, resets: () => resets };
}
test('automatic and manual readers share a physical read and a single persisted observation', async () => {
  const { adapter, read, post, reads } = setup();
  const automatic = adapter.fetchQuota(file, i18n.t);
  const manual = adapter.fetchQuota({ ...file, authIndex: 'test-index' }, i18n.t);
  expect(reads()).toBe(1);
  read.resolve(success);
  await Promise.all([automatic, manual]);
  expect(post).toHaveBeenCalledTimes(1);
});
test('reset invalidates a pending collector before persistence and UI commit while retaining its displayed balance', async () => {
  const { adapter, read, post } = setup();
  useQuotaStore.getState().setCodexQuota({ [file.name]: success });
  const generation = captureQuotaCacheGeneration(file.name);
  const automatic = adapter.fetchQuota(file, i18n.t);
  const rejection = automatic.catch((error: unknown) => error);
  await adapter.resetQuota?.(file, i18n.t);
  expect(useQuotaStore.getState().codexQuota[file.name]).toBe(success);
  post.mockClear();
  read.resolve(success);
  expect(await rejection).toMatchObject({ message: 'Quota read superseded' });
  expect(post).toHaveBeenCalledTimes(0);
  expect(commitIfQuotaCacheCurrent(generation, () => { throw new Error('obsolete UI commit'); })).toBe(false);
});
test('reset mutations are never shared', async () => {
  const { adapter, resets } = setup();
  await Promise.allSettled([adapter.resetQuota?.(file, i18n.t), adapter.resetQuota?.(file, i18n.t)]);
  expect(resets()).toBe(2);
});
test('file generation prevents sharing and persisting obsolete reads', async () => {
  const { adapter, read, post, reads } = setup();
  const old = adapter.fetchQuota(file, i18n.t);
  const rejection = old.catch((error: unknown) => error);
  useQuotaStore.getState().clearQuotaCache([file.name]);
  const current = adapter.fetchQuota(file, i18n.t);
  expect(reads()).toBe(2);
  read.resolve(success);
  expect(await rejection).toMatchObject({ message: 'Quota read superseded' }); await current;
  expect(post).toHaveBeenCalledTimes(1);
});

test('A to B to A connection switches reject the first A read before persistence', async () => {
  apiClient.setConfig({ apiBase: 'http://a.invalid', managementKey: 'synthetic-a' });
  const { adapter, read, post } = setup();
  const first = adapter.fetchQuota(file, i18n.t).catch((error: unknown) => error);
  apiClient.setConfig({ apiBase: 'http://b.invalid', managementKey: 'synthetic-b' });
  apiClient.setConfig({ apiBase: 'http://a.invalid', managementKey: 'synthetic-a' });
  const current = adapter.fetchQuota(file, i18n.t);
  read.resolve(success);
  expect(await first).toMatchObject({ message: 'Quota read superseded' });
  await current;
  expect(post).toHaveBeenCalledTimes(1);
});

test('a collector started during reset shares the confirmed reset reading', async () => {
  const mutation = deferred();
  let reads = 0;
  const post = spyOn(apiClient, 'post').mockResolvedValue({});
  const capture = spyOn(apiClient, 'captureConnection').mockReturnValue(apiClient);
  mocks.push(post, capture);
  const adapter = withQuotaHistory({
    type: 'codex', i18nPrefix: 'codex_quota', filterFn: () => true,
    fetchQuota: async () => { reads++; return success; },
    resetQuota: async () => mutation.promise,
    storeSelector: (state) => state.codexQuota, storeSetter: 'setCodexQuota',
    buildLoadingState: () => ({ status: 'loading' }),
    buildErrorState: (error) => ({ status: 'error', error }),
    buildSuccessState: () => success, Body: () => null,
  } satisfies QuotaAdapter);
  const reset = adapter.resetQuota?.(file, i18n.t);
  const collector = adapter.fetchQuota(file, i18n.t);
  mutation.resolve(success);
  expect(await collector).toBe(await reset);
  expect(reads).toBe(0);
  expect(post).toHaveBeenCalledTimes(1);
});
test('primary quota returns while its history write is pending', async () => {
  const { adapter, read, post } = setup();
  const history = deferred();
  post.mockImplementation(async () => history.promise);
  const request = adapter.fetchQuota(file, i18n.t);
  read.resolve(success);
  expect(await request).toBe(success);
  expect(post).toHaveBeenCalledTimes(1);
  history.resolve({});
});

test('a collector joined to a failed reset performs a fresh provider read', async () => {
  const mutation = deferred();
  const { adapter, read, reads, post } = setup(mutation.promise);
  const reset = adapter.resetQuota?.(file, i18n.t).catch(() => {});
  const collector = adapter.fetchQuota(file, i18n.t);
  mutation.resolve(Promise.reject(new Error('synthetic reset failure')));
  await reset;
  read.resolve(success);
  expect(await collector).toBe(success);
  expect(reads()).toBe(1);
  expect(post).toHaveBeenCalledTimes(1);
});

afterAll(() => { if (previousWindow) Object.defineProperty(globalThis, 'window', previousWindow); else Reflect.deleteProperty(globalThis, 'window'); });
