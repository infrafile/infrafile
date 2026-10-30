import type { JsonValue } from './contracts';

export type QueryName = 'list' | 'show' | 'inspect' | 'context' | 'asset-summaries' | 'changes' | 'executions' | 'execution' | 'expiry' | 'audit' | 'validate' | 'schema' | 'capabilities' | 'change/status' | 'change/preview' | 'change/apply' | 'change/recover';

export class ApiError extends Error {
  constructor(public readonly status: number) {
    super(status === 401 ? '会话已失效。请重新连接工作台。' : `无法读取此范围（HTTP ${status}）。请核对台账、历史文件与选择条件后重试。`);
    this.name = 'ApiError';
  }
}

export function parseReadJSON(text: string): JsonValue {
  try {
    return JSON.parse(text, (_key, value, context?: { source: string }) => {
      if (typeof value === 'number' && Number.isInteger(value) && !Number.isSafeInteger(value)) {
        if (context?.source) return context.source;
        throw Error('浏览器无法无损显示此数值，请使用本地 CLI。');
      }
      return value;
    });
  } catch (error) {
    if (error instanceof SyntaxError) throw Error('响应不是有效 JSON，无法读取此范围。');
    throw error;
  }
}

export function parseEditJSON(text: string): JsonValue {
  try {
    return JSON.parse(text, (_key, value) => {
      if (typeof value === 'number' && (!Number.isFinite(value) || Number.isInteger(value) && !Number.isSafeInteger(value))) throw Error('此数值超出浏览器精度，请通过 CLI 编辑。');
      return value;
    });
  } catch (error) {
    if (error instanceof SyntaxError) throw Error('记录不是有效 JSON，请核对完整记录。');
    throw error;
  }
}

export async function query<T>(name: QueryName, body: object, signal: AbortSignal, parse = parseReadJSON): Promise<T> {
  signal.throwIfAborted();
  const csrf = document.querySelector<HTMLMetaElement>('meta[name="csrf-token"]')?.content;
  if (!csrf) throw new ApiError(401);
  const response = await fetch('/api/' + name, {
    method: 'POST', credentials: 'same-origin', redirect: 'error', signal,
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(body),
  });
  if (!response.ok) throw new ApiError(response.status);
  const result = parse(await response.text());
  signal.throwIfAborted();
  return result as T;
}
