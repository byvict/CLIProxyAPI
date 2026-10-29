import { expect, test } from 'bun:test';
import { collectQuota } from '../collect-quota';

test('collects and persists weekly quota without a browser and honors shared reservations', async () => {
  expect(typeof window).toBe('undefined');
  let due = true;
  let usageReads = 0;
  const writes: unknown[] = [];
  const server = Bun.serve({ port: 0, hostname: '127.0.0.1', async fetch(request) {
    expect(request.headers.get('authorization')).toBe('Bearer synthetic');
    const path = new URL(request.url).pathname;
    if (path.endsWith('/credentials')) return Response.json({ files: [
      { name: 'account.json', type: 'codex', auth_index: 'account' },
      { name: 'disabled.json', type: 'codex', auth_index: 'disabled', disabled: true },
    ] });
    if (path.endsWith('/quota-history/poll')) {
      const claimed = due; due = false; return Response.json({ claimed });
    }
    if (path.endsWith('/requests/api-call')) {
      const body = await request.json();
      expect(body.authIndex).toBe('account');
      if (body.url.endsWith('/usage')) {
        usageReads++;
        return Response.json({ status_code: 200, body: JSON.stringify({
          rate_limit: { primary_window: { used_percent: 37, limit_window_seconds: 604800,
            reset_at: Math.floor(Date.now() / 1000) + 500000 } },
        }) });
      }
      return Response.json({ status_code: 200, body: '{}' });
    }
    if (path.endsWith('/quota-history')) {
      writes.push(await request.json()); return new Response(null, { status: 204 });
    }
    return new Response('unexpected', { status: 404 });
  } });
  try {
    const first = await collectQuota(server.url.origin, 'synthetic');
    expect(first.some((result) => result.status === 'recorded')).toBe(true);
    expect(writes).toEqual([expect.objectContaining({ authIndex: 'account', observations: [
      expect.objectContaining({ windowId: 'weekly', periodHours: 168, remaining: 63 }),
    ] })]);
    await collectQuota(server.url.origin, 'synthetic');
    expect(usageReads).toBe(1);
    expect(writes).toHaveLength(1);
  } finally { server.stop(true); }
});
