import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const settings = await readFile(new URL('../src/views/AdminSettings.vue', import.meta.url), 'utf8')
const config = await readFile(new URL('../src/stores/config.ts', import.meta.url), 'utf8')
const mark = await readFile(new URL('../src/components/BrandMark.vue', import.meta.url), 'utf8')

test('site settings provide a single brand-icon control near the site name', () => {
  assert.match(settings, /站点名称/)
  assert.match(settings, /修改图标/)
  assert.match(settings, /上传 PNG \/ JPEG \/ WebP/)
  assert.match(settings, /brand_icon_data_uri/)
  assert.match(settings, /最大 512 KiB/)
})

test('public branding synchronizes the visual mark and browser icons', () => {
  assert.match(mark, /config\.config\.brand_icon_data_uri \|\| '\/qingzhou-mark\.svg'/)
  assert.match(config, /document\.title = name/)
  assert.match(config, /'shortcut icon'/)
  assert.match(config, /'apple-touch-icon'/)
})
