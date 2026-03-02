"use client";

import {
  createContext,
  useContext,
  useRef,
  useCallback,
  type ReactNode,
} from "react";

interface CacheEntry {
  data: unknown;
  fetchedAt: number;
}

interface CacheContextValue {
  get: <T>(key: string) => T | undefined;
  set: (key: string, data: unknown) => void;
  invalidatePrefix: (prefix: string) => void;
  invalidateAll: () => void;
}

const CacheContext = createContext<CacheContextValue | null>(null);

const MAX_AGE_MS = 5 * 60 * 1000; // 5 minutes staleness

export function PortfolioCacheProvider({ children }: { children: ReactNode }) {
  const store = useRef(new Map<string, CacheEntry>());

  const get = useCallback(<T,>(key: string): T | undefined => {
    const entry = store.current.get(key);
    if (!entry) return undefined;
    if (Date.now() - entry.fetchedAt > MAX_AGE_MS) {
      store.current.delete(key);
      return undefined;
    }
    return entry.data as T;
  }, []);

  const set = useCallback((key: string, data: unknown) => {
    store.current.set(key, { data, fetchedAt: Date.now() });
  }, []);

  const invalidatePrefix = useCallback((prefix: string) => {
    for (const key of store.current.keys()) {
      if (key.startsWith(prefix)) store.current.delete(key);
    }
  }, []);

  const invalidateAll = useCallback(() => {
    store.current.clear();
  }, []);

  return (
    <CacheContext.Provider value={{ get, set, invalidatePrefix, invalidateAll }}>
      {children}
    </CacheContext.Provider>
  );
}

export function usePortfolioCache() {
  const ctx = useContext(CacheContext);
  if (!ctx) throw new Error("usePortfolioCache must be inside PortfolioCacheProvider");
  return ctx;
}

export function useCachedFetch<T>(cacheKey: string) {
  const cache = usePortfolioCache();

  const fetchWithCache = useCallback(
    async (url: string, opts?: { force?: boolean }): Promise<T | null> => {
      if (!opts?.force) {
        const cached = cache.get<T>(cacheKey);
        if (cached !== undefined) return cached;
      }

      const res = await fetch(url);
      if (!res.ok) return null;
      const data: T = await res.json();
      cache.set(cacheKey, data);
      return data;
    },
    [cache, cacheKey]
  );

  const getCached = useCallback((): T | undefined => {
    return cache.get<T>(cacheKey);
  }, [cache, cacheKey]);

  return { fetchWithCache, getCached, invalidateAll: cache.invalidateAll };
}
