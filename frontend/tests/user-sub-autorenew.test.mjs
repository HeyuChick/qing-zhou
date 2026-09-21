import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const source = await readFile(new URL('../src/views/UserSub.vue', import.meta.url), 'utf8')

test('subscription cards provide an automatic renewal control', () => {
  assert.match(source, /NCheckbox/)
  assert.match(source, />自动续订<\/n-checkbox>/)
  assert.match(source, /auto_renew/)
  assert.match(source, /\/api\/user\/plans\/\$\{line\.autoRenewPlanID\}\/auto-renew/)
  assert.match(source, /renewSaving/)
})
