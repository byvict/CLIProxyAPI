import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createInstance } from 'i18next';
import { apiClient } from './src/services/api/client';
import { authFilesApi } from './src/services/api/authFiles';
import { ANTIGRAVITY_CONFIG } from './src/features/quota/providers/antigravity/data';
import { CLAUDE_CONFIG } from './src/features/quota/providers/claude/data';
import { CODEX_CONFIG } from './src/features/quota/providers/codex/data';
import { DEVIN_CONFIG } from './src/features/quota/providers/devin/data';
import { KIMI_CONFIG } from './src/features/quota/providers/kimi/data';
import { MetaQuotaError } from './src/features/quota/providers/meta/requests';
import { META_CONFIG } from './src/features/quota/providers/meta/data';
import { XAI_CONFIG } from './src/features/quota/providers/xai/data';
import type { QuotaProviderData } from './src/features/quota/providers/types';
import { quotaObservations } from './src/features/quotaHistory/observations';
import { reserveQuotaPoll, recordQuotaHistory } from './src/features/quotaHistory/api';
import { normalizeAuthIndex } from './src/utils/authIndex';
import type { AuthFileItem } from './src/types';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: {} });

function reader<State, Data>(config: QuotaProviderData<State, Data>) {
  return {
    type: config.type,
    accepts: config.filterFn,
    async collect(file: AuthFileItem) {
      const index = normalizeAuthIndex(file.authIndex ?? file.auth_index);
      if (!index || file.disabled) return { status: 'disabled' };
      if (!(await reserveQuotaPoll(index, apiClient, true))) return { status: 'not-due' };
      const data = await config.fetchQuota(file, i18n.t, apiClient.captureConnection());
      const observations = quotaObservations(index, config.type, config.buildSuccessState(data), Date.now());
      if (observations.length) await recordQuotaHistory(index, observations);
      return { status: 'recorded', observations: observations.length };
    },
  };
}
const readers = [reader(ANTIGRAVITY_CONFIG), reader(CLAUDE_CONFIG), reader(CODEX_CONFIG),
  reader(DEVIN_CONFIG), reader(KIMI_CONFIG), reader(META_CONFIG), reader(XAI_CONFIG)];

export async function collectQuota(apiBase: string, managementKey: string) {
  apiClient.setConfig({ apiBase, managementKey });
  const { files } = await authFilesApi.list();
  return Promise.all(files.map(async (file) => {
    const adapter = readers.find((candidate) => candidate.accepts(file));
    if (!adapter || file.disabled) return { provider: file.type, status: 'disabled' };
    try {
      return { provider: adapter.type, ...await adapter.collect(file) };
    } catch (error: unknown) {
      return { provider: adapter.type, status: 'error',
        reason: error instanceof MetaQuotaError ? error.code : 'request_failed' };
    }
  }));
}

if (import.meta.main) {
  const [runtime, receipt] = process.argv.slice(2);
  if (!runtime || !receipt) throw new Error('Usage: collect-quota <runtime> <receipt>');
  const key = (await readFile(join(runtime, 'management-key.txt'), 'utf8')).trim();
  const results = await collectQuota('http://127.0.0.1:8317', key);
  await writeFile(receipt, JSON.stringify({ at: new Date().toISOString(), results }, null, 2) + '\n', { mode: 0o600 });
  if (results.some((result) => result.status === 'error')) process.exitCode = 1;
}
