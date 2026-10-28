export function mountV1History(
  shadow: ShadowRoot,
  request: (path: string, options?: { method?: string; body?: string; signal?: AbortSignal }) => Promise<unknown>,
  refresh: () => void
): () => void;
