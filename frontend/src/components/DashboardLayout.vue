<template>
  <div class="app-shell" :class="{ mobile: isMobile }">
    <!-- 桌面侧边栏 -->
    <aside v-if="!isMobile" class="app-sider">
      <div class="sidebar-brand" @click="router.push('/')">
        <div class="sidebar-logo">鸡</div>
        <span class="brand-text">{{ config.config.site_name || '轻舟' }}</span>
      </div>
      <nav class="sidebar-menu">
        <n-menu :value="activeKey" :options="menuOptions" :default-expanded-keys="['admin-root']" :indent="18" @update:value="handleMenuSelect" />
      </nav>
    </aside>

    <!-- 移动端抽屉 -->
    <n-drawer v-model:show="drawerShow" placement="left" :width="260" :block-scroll="true">
      <n-drawer-content :native-scrollbar="true" body-content-style="padding:0;">
        <div class="sidebar-brand" @click="goAndClose('/')">
          <div class="sidebar-logo">鸡</div>
          <span class="brand-text">{{ config.config.site_name || '轻舟' }}</span>
        </div>
        <nav class="sidebar-menu">
          <n-menu :value="activeKey" :options="menuOptions" :default-expanded-keys="['admin-root']" :indent="18" @update:value="goAndClose" />
        </nav>
      </n-drawer-content>
    </n-drawer>

    <!-- 主区 -->
    <div class="app-main">
      <header class="layout-header">
        <div class="header-left">
          <button v-if="isMobile" class="icon-btn" @click="drawerShow = true" aria-label="菜单">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/></svg>
          </button>
          <span class="header-title">{{ currentTitle }}</span>
        </div>
        <div class="header-right">
          <template v-if="auth.isAdmin && !isMobile">
            <n-dropdown :options="adminQuickMenu" @select="handleAdminSelect">
              <n-button quaternary size="small">管理</n-button>
            </n-dropdown>
          </template>
          <n-dropdown :options="userMenu" @select="handleUserSelect">
            <n-button quaternary size="small">
              <template #icon><n-icon><PersonOutline /></n-icon></template>
              {{ isMobile ? '' : auth.user?.username }}
            </n-button>
          </n-dropdown>
        </div>
      </header>
      <main class="layout-content">
        <router-view />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { h, computed, ref, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NDrawer, NDrawerContent, NButton, NDropdown, NIcon, NMenu } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import {
  SpeedometerOutline, LinkOutline, CartOutline,
  ReceiptOutline, WalletOutline, MegaphoneOutline, BookOutline,
  PersonOutline, PeopleOutline, PeopleCircleOutline, ArchiveOutline, ServerOutline,
  SettingsOutline, KeyOutline, NotificationsOutline, DocumentTextOutline,
  PulseOutline, HardwareChipOutline, HomeOutline, LogOutOutline, CloudDownloadOutline,
  ShieldCheckmarkOutline
} from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { useConfigStore } from '@/stores/config'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const config = useConfigStore()

const activeKey = computed(() => route.path)

// ---- 响应式：移动端判定 ----
const isMobile = ref(false)
const drawerShow = ref(false)

function checkMobile() {
  isMobile.value = window.matchMedia('(max-width: 768px)').matches
  if (!isMobile.value) drawerShow.value = false
}
onMounted(() => { checkMobile(); window.addEventListener('resize', checkMobile) })
onUnmounted(() => window.removeEventListener('resize', checkMobile))

function renderIcon(icon: any) {
  return () => h(NIcon, null, { default: () => h(icon) })
}

function groupLabel(text: string) {
  return () => h('span', { class: 'menu-group-label' }, text)
}

const userMenuItems: MenuOption[] = [
  { label: '控制台', key: '/dashboard', icon: renderIcon(SpeedometerOutline) },
  { label: '订阅管理', key: '/sub', icon: renderIcon(LinkOutline) },
]

const shopItems: MenuOption[] = [
  { label: '积分商城', key: '/shop', icon: renderIcon(CartOutline) },
  { label: '订单记录', key: '/orders', icon: renderIcon(ReceiptOutline) },
  { label: '积分明细', key: '/points', icon: renderIcon(WalletOutline) },
]

const infoItems: MenuOption[] = [
  { label: '公告通知', key: '/notices', icon: renderIcon(MegaphoneOutline) },
  { label: '帮助中心', key: '/help', icon: renderIcon(BookOutline) },
]

const adminOpsItems: MenuOption[] = [
  { label: '管理概览', key: '/admin', icon: renderIcon(SpeedometerOutline) },
  { label: '用户管理', key: '/admin/users', icon: renderIcon(PeopleOutline) },
  { label: '用户组', key: '/admin/user-groups', icon: renderIcon(PeopleCircleOutline) },
  { label: '套餐管理', key: '/admin/packages', icon: renderIcon(ArchiveOutline) },
  { label: '订单管理', key: '/admin/orders', icon: renderIcon(ReceiptOutline) },
  { label: '注册码', key: '/admin/reg-codes', icon: renderIcon(KeyOutline) },
]
const adminNodeItems: MenuOption[] = [
  { label: '节点管理', key: '/admin/nodes', icon: renderIcon(ServerOutline) },
  { label: 'sing-box', key: '/admin/singbox', icon: renderIcon(HardwareChipOutline) },
  { label: '证书管理', key: '/admin/certs', icon: renderIcon(ShieldCheckmarkOutline) },
  { label: '服务器', key: '/admin/servers', icon: renderIcon(ServerOutline) },
  { label: '监控管理', key: '/admin/monitor', icon: renderIcon(PulseOutline) },
]
const adminSysItems: MenuOption[] = [
  { label: '公告管理', key: '/admin/announcements', icon: renderIcon(NotificationsOutline) },
  { label: '帮助文档', key: '/admin/help', icon: renderIcon(DocumentTextOutline) },
  { label: '系统设置', key: '/admin/settings', icon: renderIcon(SettingsOutline) },
  { label: '在线更新', key: '/admin/update', icon: renderIcon(CloudDownloadOutline) },
]

const menuOptions = computed<MenuOption[]>(() => {
  const items: MenuOption[] = [
    { label: '首页', key: '/', icon: renderIcon(HomeOutline) },
    { type: 'group', key: 'g-common', label: groupLabel('常用'), children: userMenuItems },
    { type: 'group', key: 'g-shop', label: groupLabel('商城'), children: shopItems },
    { type: 'group', key: 'g-info', label: groupLabel('信息'), children: infoItems },
    { label: '账户设置', key: '/account', icon: renderIcon(PersonOutline) },
  ]
  if (auth.isAdmin) {
    items.push({
      label: '管理后台', key: 'admin-root', icon: renderIcon(SettingsOutline),
      children: [
        { type: 'group', key: 'ag-ops', label: groupLabel('运营'), children: adminOpsItems },
        { type: 'group', key: 'ag-node', label: groupLabel('节点服务'), children: adminNodeItems },
        { type: 'group', key: 'ag-sys', label: groupLabel('内容系统'), children: adminSysItems },
      ],
    })
  }
  return items
})

const titleMap: Record<string, string> = {
  '/': '首页', '/dashboard': '控制台', '/sub': '订阅管理', '/shop': '积分商城',
  '/orders': '订单记录', '/points': '积分明细', '/notices': '公告通知', '/help': '帮助中心', '/account': '账户设置',
  '/admin': '管理概览', '/admin/users': '用户管理', '/admin/user-groups': '用户组', '/admin/packages': '套餐管理', '/admin/nodes': '节点管理',
  '/admin/singbox': 'sing-box', '/admin/certs': '证书管理', '/admin/orders': '订单管理', '/admin/servers': '服务器', '/admin/monitor': '监控管理',
  '/admin/settings': '系统设置', '/admin/reg-codes': '注册码', '/admin/announcements': '公告管理', '/admin/help': '帮助文档',
  '/admin/update': '在线更新',
}
const currentTitle = computed(() => titleMap[route.path] || config.config.site_name || '轻舟')

const userMenu = [
  { label: '退出登录', key: 'logout', icon: () => h(NIcon, null, { default: () => h(LogOutOutline) }) },
]
const adminQuickMenu = [
  { label: '管理概览', key: '/admin' },
  { label: '用户管理', key: '/admin/users' },
  { label: '系统设置', key: '/admin/settings' },
]

function handleMenuSelect(key: string) {
  if (key === 'admin-root') return
  router.push(key)
}
function goAndClose(key: string) {
  if (key === 'admin-root') return
  router.push(key)
  drawerShow.value = false
}
function handleUserSelect(key: string) {
  if (key === 'logout') { auth.logout(); router.push('/') }
}
function handleAdminSelect(key: string) { router.push(key) }
</script>

<style scoped>
.app-shell { display: flex; min-height: 100vh; }
.app-sider {
  width: 220px; flex-shrink: 0;
  background: var(--bg-soft);
  border-right: 1px solid var(--border);
  position: sticky; top: 0; height: 100vh;
  display: flex; flex-direction: column;
}
.sidebar-brand {
  display: flex; align-items: center; gap: 10px;
  padding: 16px 16px 12px;
  font-weight: 750; font-size: 17px; cursor: pointer;
  letter-spacing: -0.02em;
}
.sidebar-logo {
  width: 30px; height: 30px; border-radius: 9px;
  background: linear-gradient(135deg, var(--accent), var(--accent-strong));
  display: grid; place-items: center; color: #fff; font-size: 16px; font-weight: 700;
  flex-shrink: 0;
}
.brand-text { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sidebar-menu { flex: 1; overflow-y: auto; padding-bottom: 16px; }

.app-main { flex: 1; min-width: 0; display: flex; flex-direction: column; min-height: 100vh; }
.layout-header {
  height: 56px; display: flex; align-items: center; justify-content: space-between;
  padding: 0 24px;
  border-bottom: 1px solid var(--border);
  background: rgba(250, 250, 250, 0.85);
  backdrop-filter: blur(8px);
  position: sticky; top: 0; z-index: 10;
}
.header-left { display: flex; align-items: center; gap: 10px; min-width: 0; }
.header-title { font-weight: 650; font-size: 15px; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.header-right { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.icon-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 36px; height: 36px; border-radius: 9px; border: 1px solid var(--border);
  background: #fff; color: var(--text-2); cursor: pointer; padding: 0;
}
.icon-btn:hover { background: var(--bg-soft); }

.layout-content { flex: 1; padding: 24px; max-width: 1080px; margin: 0 auto; width: 100%; box-sizing: border-box; }

/* 移动端 */
.app-shell.mobile .layout-header { padding: 0 12px; }
.app-shell.mobile .layout-content { padding: 14px 12px; }

/* 分组标签样式 */
:deep(.menu-group-label) {
  font-size: 11px; font-weight: 600; color: var(--text-3);
  letter-spacing: 0.04em;
}
:deep(.n-menu-item-group-title) { padding: 12px 16px 4px !important; }
:deep(.n-menu-item-content) { border-radius: 8px; margin: 1px 8px; }
</style>
