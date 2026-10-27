export function mountV1History(
  shadow: ShadowRoot,
  request: (path: string, options?: { method?: string; body?: string }) => Promise<unknown>,
  refresh: () => void
): () => void;
