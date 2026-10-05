import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
for(const [file,constant] of [['AdminSingbox.vue','RELAY_LANDING_TYPES'],['AdminNodes.vue','routeLandingTypes']]){
 test(`${file} exposes every native landing protocol and excludes mixed`,async()=>{
  const source=await readFile(new URL(`../src/views/${file}`,import.meta.url),'utf8')
  const line=source.split('\n').find(line=>line.startsWith(`const ${constant} =`))
  assert.ok(line)
  for(const protocol of ['vless','vmess','trojan','shadowsocks','hysteria','hysteria2','tuic','anytls']) assert.ok(line.includes(`'${protocol}'`),`${protocol} not selectable`)
  assert.ok(!line.includes("'mixed'"))
 })
}
