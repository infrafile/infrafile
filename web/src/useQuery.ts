import { useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export type QueryState<T> = { status: 'idle' | 'loading' } | { status: 'error'; error: Error } | { status: 'ready'; data: T };

export function useQuery<T>(requestKey: string | null, load: (signal: AbortSignal) => Promise<T>, onUnauthorized: () => void): QueryState<T> {
  const sequence = useRef(0);
  const [result, setResult] = useState<{ key: string | null; state: QueryState<T> }>({ key: null, state: { status: 'idle' } });
  useEffect(() => {
    const id = ++sequence.current;
    if (requestKey === null) return;
    const controller = new AbortController();
    setResult({ key: requestKey, state: { status: 'loading' } });
    load(controller.signal).then(data => {
      if (!controller.signal.aborted && id === sequence.current) setResult({ key: requestKey, state: { status: 'ready', data } });
    }).catch((error: unknown) => {
      // A received 401 invalidates the session even when its old selection is no longer visible.
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      if (controller.signal.aborted || id !== sequence.current) return;
      setResult({ key: requestKey, state: { status: 'error', error: error instanceof Error ? error : Error('无法读取此范围。') } });
    });
    return () => { controller.abort(); ++sequence.current; };
  }, [requestKey, load, onUnauthorized]);
  if (requestKey === null) return { status: 'idle' };
  // Never expose the previous selection in the render before the new effect runs.
  return result.key === requestKey ? result.state : { status: 'loading' };
}
