import { expect, it } from 'vitest';
import { pageURL, readScope } from './scope';

it('defaults to seven days and preserves an explicit zero', () => {
  expect(readScope('')).toEqual({ key: '', days: 7, issue: '' });
  expect(readScope('#key=Server%2Fweb&days=0&issue=one%2Ftwo')).toEqual({ key: 'Server/web', days: 0, issue: 'one/two' });
});
it.each(['-1', '1.5', 'NaN', 'Infinity', '9007199254740993', '2e2'])('rejects invalid TTL %s', value => {
  expect(() => readScope('#days=' + value)).toThrow('证据有效期');
});
it('preserves the selected asset and issue across page links', () => {
  expect(pageURL('changes', { key: 'Domain/example.net', days: 7, issue: 'fingerprint/scope' })).toBe('/changes#days=7&key=Domain%2Fexample.net&issue=fingerprint%2Fscope');
});
it('only carries global instance targets into changes, not asset-scoped APIs', () => {
  const scope = { key: 'dns/public', days: 7, issue: '' };
  expect(pageURL('assets', scope)).toBe('/assets#days=7');
  expect(pageURL('inspections', scope)).toBe('/inspections#days=7');
  expect(pageURL('changes', scope)).toBe('/changes#days=7&key=dns%2Fpublic');
});
