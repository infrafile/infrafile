import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const schema = JSON.parse(readFileSync(new URL('../internal/ledger/schemas/common.schema.json', import.meta.url)));
const secret = schema.$defs.secrets.additionalProperties;
// Editors differ on Unicode mode; both must enforce the same reference syntax.
for (const flags of ['', 'u']) {
  const pattern = new RegExp(secret.pattern, flags);
  const valid = value => pattern.test(value);
  for (const value of ['env://TOKEN', 'sops://secrets.enc.yaml#accounts.main.key', 'sops://my%20secrets.enc.yaml#key', 'op://My Vault/Item/Field', 'op://Vault/Item/Section/Field']) {
    assert(valid(value), `valid reference rejected (${flags}): ${value}`);
  }
  for (const value of ['plain-text', 'env://1TOKEN', 'sops://a b#key', 'sops://a\\b#key', 'sops://a#b?c', 'op://vault/item/field/extra/extra']) {
    assert(!valid(value), `invalid reference accepted (${flags}): ${value}`);
  }
  for (let n = 0; n <= 0x9f; n++) {
    if (n > 0x1f && n < 0x7f) continue;
    const c = String.fromCodePoint(n);
    for (const value of [`sops://a${c}b#key`, `op://vault/item/fi${c}eld`, `env://TOKEN${c}`]) assert(!valid(value));
  }
  for (const n of [0x20, 0xa0, 0x1680, ...Array.from({length: 11}, (_, i) => 0x2000 + i), 0x2028, 0x2029, 0x202f, 0x205f, 0x3000]) {
    const c = String.fromCodePoint(n);
    assert(!valid(`sops://a${c}b#key`));
    assert(!valid(`sops://a#key${c}`));
  }
}
console.log('Schema credential patterns pass in JavaScript with and without Unicode mode');
