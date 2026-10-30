import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, parseReadJSON, query } from './api';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('lossless read JSON', () => {
  it('preserves large cursors and normal data without silently rounding', () => {
    expect(parseReadJSON('{"last_sequence":9007199254740993,"count":7,"fraction":1.5,"description":"<img src=x>"}')).toEqual({ last_sequence: '9007199254740993', count: 7, fraction: 1.5, description: '<img src=x>' });
  });
  it('refuses unsafe integers if the browser lacks reviver source support', () => {
    const original = JSON.parse;
    vi.spyOn(JSON, 'parse').mockImplementation((text, reviver) => original(text, reviver && ((key, value) => reviver(key, value))));
    expect(parseReadJSON('{"count":7}')).toEqual({ count: 7 });
    expect(() => parseReadJSON('{"sequence":9007199254740993}')).toThrow('无法无损');
  });
  it('does not echo a malformed response into the error', () => {
    expect(() => parseReadJSON('private-fragment invalid JSON')).toThrow('响应不是有效 JSON');
  });
});

describe('workbench request boundary', () => {
  function setup(response: Response) {
    vi.stubGlobal('document', { querySelector: () => ({ content: 'test-csrf' }) });
    const fetch = vi.fn(async () => response);
    vi.stubGlobal('fetch', fetch);
    return fetch;
  }
  it('uses a same-origin POST with CSRF, no redirects, and the caller signal', async () => {
    const fetch = setup(new Response('{"ok":false}'));
    const signal = new AbortController().signal;
    expect(await query('inspect', { max_age_days: 7 }, signal)).toEqual({ ok: false });
    expect(fetch).toHaveBeenCalledWith('/api/inspect', expect.objectContaining({ method: 'POST', credentials: 'same-origin', redirect: 'error', signal, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'test-csrf' }, body: '{"max_age_days":7}' }));
  });
  it.each([401, 403, 422, 429, 504])('preserves HTTP %s without reflecting response text', async status => {
    setup(new Response('private server error', { status }));
    await expect(query('list', {}, new AbortController().signal)).rejects.toMatchObject({ status });
    try { await query('list', {}, new AbortController().signal); } catch (error) { expect(error).toBeInstanceOf(ApiError); expect(String(error)).not.toContain('private server error'); }
  });
  it('does not start an already cancelled request', async () => {
    const fetch = setup(new Response('{}'));
    const controller = new AbortController(); controller.abort();
    await expect(query('context', { key: 'Server/a' }, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).not.toHaveBeenCalled();
  });
  it('discards a response cancelled while its body was being read', async () => {
    const controller = new AbortController();
    setup(new Response('{}'));
    vi.stubGlobal('fetch', async () => ({ ok: true, status: 200, text: async () => { controller.abort(); return '{}'; } }));
    await expect(query('list', {}, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
  });
});
