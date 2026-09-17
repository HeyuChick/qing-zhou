import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/views/AdminSettings.vue', import.meta.url), 'utf8')
const api = await readFile(new URL('../src/api/index.ts', import.meta.url), 'utf8')

 test('data backup settings exposes local snapshot and remote object storage controls', () => {
  assert.match(source, /settings-backup/)
  assert.match(source, /Cloudflare R2 或其他 S3 兼容对象存储/)
  assert.match(source, /backup_s3_config|\/api\/admin\/backups\/config/)
  assert.match(source, /Secret Access Key/)
  assert.match(source, /留空保留已保存密钥/)
  assert.match(source, /测试连接/)
  assert.match(source, /保存自动备份计划/)
  assert.match(source, /立即备份到远端/)
  assert.match(source, /download-url/)
  assert.match(source, /删除远端备份/)
 })

 test('remote backup UI keeps secrets out of readback and uses authenticated delete API', () => {
  assert.match(source, /backupConfig\.secret_access_key = ''/)
  assert.match(source, /apiDelete\(`\/api\/admin\/backups\/\$\{record\.id\}`\)/)
  assert.match(api, /export function apiDelete/)
 })

test('disaster recovery mode explains unencrypted scope and preserves status when saving config', () => {
  assert.match(source, /未额外加密的灾备恢复包/)
  assert.match(source, /QZ_BACKUP_MANIFEST/)
  assert.match(source, /backupRecovery\.sources/)
  assert.match(source, /if \(data\?\.recovery\) backupRecovery\.value = data\.recovery/)
  assert.match(source, /record\.format === 'tar.gz'/)
})
