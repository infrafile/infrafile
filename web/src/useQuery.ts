import { useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export type QueryState<T> = { status: 'idle' | 'loading' } | { status: 'error'; error: Error } | { status: 'ready'; data: T };

export interface AutoReadState {
  enabled: boolean; setEnabled: (enabled: boolean) => void;
  reading: boolean; readAt: string | null; error: Error | null; failures: number;
  pendingCount: number; apply: () => void;
}
type ReadMeta = Pick<AutoReadState, 'reading' | 'readAt' | 'error' | 'failures' | 'pendingCount'>;
const emptyMeta = (): ReadMeta => ({ reading: false, readAt: null, error: null, failures: 0, pendingCount: 0 });
type ReadOptions<T> = { automatic: boolean; changes?: (previous: T, next: T) => number };

export function useQuery<T>(requestKey: string | null, load: (signal: AbortSignal, automatic?: boolean) => Promise<T>, onUnauthorized: () => void, options?: ReadOptions<T>): QueryState<T> & { auto: AutoReadState } {
  const sequence = useRef(0);
  const [enabled, setEnabled] = useState(true);
  const settings = useRef(options); settings.current = options;
  const reader = useRef<{ run: () => boolean; cancel: () => void; apply: () => void; lastAttempt: number } | null>(null);
  const [result, setResult] = useState<{ key: string | null; state: QueryState<T>; meta: ReadMeta }>({ key: null, state: { status: 'idle' }, meta: emptyMeta() });
  useEffect(() => {
    const id = ++sequence.current;
    if (requestKey === null) return;
    let flight: { controller: AbortController; automatic: boolean } | null = null;
    let current: { data: T } | undefined, pending: { data: T; at: string } | undefined;
    let failures = 0;
    const read = async (automatic: boolean) => {
      if (flight) return;
      const operation = { controller: new AbortController(), automatic };
      flight = operation;
      entry.lastAttempt = Date.now();
      if (automatic) setResult(previous => ({ ...previous, meta: { ...previous.meta, reading: true } }));
      try {
        const data = await load(operation.controller.signal, automatic);
        if (operation.controller.signal.aborted || id !== sequence.current) return;
        failures = 0;
        const at = new Date().toISOString();
        const count = automatic && current ? settings.current?.changes?.(current.data, data)
          ?? (JSON.stringify(current.data, (key, value) => key === 'as_of' ? undefined : value) === JSON.stringify(data, (key, value) => key === 'as_of' ? undefined : value) ? 0 : 1) : 0;
        pending = count ? { data, at } : undefined;
        if (count) setResult(previous => ({ ...previous, meta: { ...previous.meta, reading: false, error: null, failures: 0, pendingCount: count } }));
        else {
          current = { data };
          setResult({ key: requestKey, state: { status: 'ready', data }, meta: { ...emptyMeta(), readAt: at } });
        }
      } catch (error: unknown) {
        // A received 401 invalidates the session even when its old selection is no longer visible.
        if (error instanceof ApiError && error.status === 401) onUnauthorized();
        if (operation.controller.signal.aborted || id !== sequence.current) return;
        const failure = error instanceof Error ? error : Error('无法读取此范围。');
        pending = undefined;
        if (automatic && current) setResult(previous => ({ ...previous, meta: { ...previous.meta, reading: false, error: failure, failures: ++failures, pendingCount: 0 } }));
        else setResult({ key: requestKey, state: { status: 'error', error: failure }, meta: emptyMeta() });
      } finally {
        if (flight === operation) flight = null;
      }
    };
    const entry = {
      lastAttempt: 0,
      run: () => { if (flight) return false; void read(true); return true; },
      cancel: () => {
        if (flight?.automatic) {
          flight.controller.abort(); flight = null;
          setResult(previous => ({ ...previous, meta: { ...previous.meta, reading: false } }));
        }
      },
      apply: () => {
        if (!pending) return;
        current = { data: pending.data };
        setResult({ key: requestKey, state: { status: 'ready', data: pending.data }, meta: { ...emptyMeta(), reading: !!flight, readAt: pending.at } });
        pending = undefined;
      },
    };
    reader.current = entry;
    setResult({ key: requestKey, state: { status: 'loading' }, meta: emptyMeta() });
    void read(false);
    return () => { flight?.controller.abort(); ++sequence.current; if (reader.current === entry) reader.current = null; };
  }, [requestKey, load, onUnauthorized]);
  const automatic = !!options?.automatic && enabled;
  useEffect(() => {
    if (!automatic || requestKey === null) return;
    const attempt = (minimumAge: number) => {
      const entry = reader.current;
      return document.visibilityState !== 'hidden' && entry && Date.now() - entry.lastAttempt >= minimumAge ? entry.run() : false;
    };
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => { clearTimeout(timer); timer = setTimeout(() => { attempt(60000); schedule(); }, 60000); };
    const resume = () => { if (document.visibilityState === 'hidden') reader.current?.cancel(); else if (attempt(30000)) schedule(); };
    schedule();
    window.addEventListener('focus', resume); document.addEventListener('visibilitychange', resume);
    return () => { clearTimeout(timer); window.removeEventListener('focus', resume); document.removeEventListener('visibilitychange', resume); reader.current?.cancel(); };
  }, [automatic, requestKey]);
  const meta = result.key === requestKey ? result.meta : emptyMeta();
  const auto: AutoReadState = { ...meta, enabled, setEnabled, apply: () => reader.current?.apply() };
  if (requestKey === null) return { status: 'idle', auto };
  // Never expose the previous selection in the render before the new effect runs.
  return { ...(result.key === requestKey ? result.state : { status: 'loading' as const }), auto };
}
