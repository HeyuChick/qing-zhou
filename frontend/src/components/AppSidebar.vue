<template>
  <n-layout has-sider style="min-height: 100vh;">
    <n-layout-sider
      bordered
      :width="220"
      :collapsed-width="0"
      show-trigger="bar"
      collapse-mode="transform"
      :native-scrollbar="false"
      style="background: var(--bg-soft);"
    >
      <div class="sidebar-brand" @click="router.push('/')">
        <div class="sidebar-logo">舟</div>
        <span>{{ config.config.site_name || '轻舟' }}</span>
      </div>
      <n-menu
        :value="activeKey"
        :options="menuOptions"
        :collapsed-width="0"
        @update:value="handleMenuSelect"
      />
    </n-layout-sider>
    <n-layout>
      <AppHeader />
      <n-layout-content content-style="padding: 24px; max-width: 1080px; margin: 0 auto;">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { h, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NLayout, NLayoutSider, NLayoutContent, NMenu, NIcon } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import {
  HomeOutline, SpeedometerOutline, LinkOutline, CartOutline,
  ReceiptOutline, WalletOutline, MegaphoneOutline, BookOutline,
  PersonOutline, PeopleOutline, ArchiveOutline, ServerOutline,
  SettingsOutline, KeyOutline, NotificationsOutline, DocumentTextOutline,
  PulseOutline, HardwareChipOutline
} from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { useConfigStore } from '@/stores/config'
import AppHeader from '@/components/AppHeader.vue'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const config = useConfigStore()

const activeKey = computed(() => route.path)

function renderIcon(icon: any) {
  return () => h(NIcon, null, { default: () => h(icon) })
}

const userMenuItems: MenuOption[] = [
  { label: '控制台', key: '/dashboard', icon: renderIcon(SpeedometerOutline) },
  { label: '订阅管理', key: '/sub', icon: renderIcon(LinkOutline) },
  { label: '积分商城', key: '/shop', icon: renderIcon(CartOutline) },
  { label: '订单记录', key: '/orders', icon: renderIcon(ReceiptOutline) },
  { label: '积分明细', key: '/points', icon: renderIcon(WalletOutline) },
  { label: '公告通知', key: '/notices', icon: renderIcon(MegaphoneOutline) },
  { label: '帮助中心', key: '/help', icon: renderIcon(BookOutline) },
  { label: '账户设置', key: '/account', icon: renderIcon(PersonOutline) },
]

const adminMenuItems: MenuOption[] = [
  { label: '管理概览', key: '/admin', icon: renderIcon(SpeedometerOutline) },
  { label: '用户管理', key: '/admin/users', icon: renderIcon(PeopleOutline) },
  { label: '套餐管理', key: '/admin/packages', icon: renderIcon(ArchiveOutline) },
  { label: '节点管理', key: '/admin/nodes', icon: renderIcon(ServerOutline) },
  { label: 'sing-box', key: '/admin/singbox', icon: renderIcon(HardwareChipOutline) },
  { label: '服务器', key: '/admin/servers', icon: renderIcon(ServerOutline) },
  { label: '监控管理', key: '/admin/monitor', icon: renderIcon(PulseOutline) },
  { label: '订单管理', key: '/admin/orders', icon: renderIcon(ReceiptOutline) },
  { label: '注册码', key: '/admin/reg-codes', icon: renderIcon(KeyOutline) },
  { label: '公告管理', key: '/admin/announcements', icon: renderIcon(NotificationsOutline) },
  { label: '帮助文档', key: '/admin/help', icon: renderIcon(DocumentTextOutline) },
  { label: '系统设置', key: '/admin/settings', icon: renderIcon(SettingsOutline) },
]

const menuOptions = computed<MenuOption[]>(() => {
  const items: MenuOption[] = [
    { label: '首页', key: '/', icon: renderIcon(HomeOutline) },
    { type: 'divider', key: 'd-user', props: { style: { margin: '8px 16px' } } },
    ...userMenuItems,
  ]
  if (auth.isAdmin) {
    items.push(
      { type: 'divider', key: 'd-admin', props: { style: { margin: '8px 16px' } } },
      { label: '管理后台', key: 'admin-header', icon: renderIcon(SettingsOutline), children: adminMenuItems },
    )
  }
  return items
})

function handleMenuSelect(key: string) {
  if (key === 'admin-header') return
  router.push(key)
}
</script>

<style scoped>
.sidebar-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 16px 12px;
  font-weight: 750;
  font-size: 17px;
  cursor: pointer;
  letter-spacing: -0.02em;
}
.sidebar-logo {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  background: linear-gradient(135deg, var(--accent), var(--accent-strong));
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 16px;
  font-weight: 700;
}
</style>
