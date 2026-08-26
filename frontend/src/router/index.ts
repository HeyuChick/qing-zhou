import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      name: 'monitor',
      component: () => import('@/views/Monitor.vue'),
      // 品牌定制：首页（监控大屏）仅管理员可见；登录的普通用户去控制台，
      // 未登录去独立登录页（守卫也会把受保护页送到那里）
      beforeEnter: () => {
        const auth = useAuthStore()
        if (!auth.isAdmin) {
          return auth.isLoggedIn ? { name: 'dashboard' } : { name: 'login' }
        }
      },
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
    },
    {
      path: '/',
      component: () => import('@/components/DashboardLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: 'dashboard', name: 'dashboard', component: () => import('@/views/UserDashboard.vue') },
        { path: 'sub', name: 'sub', component: () => import('@/views/UserSub.vue') },
        { path: 'shop', name: 'shop', component: () => import('@/views/UserShop.vue') },
        { path: 'orders', name: 'orders', component: () => import('@/views/UserOrders.vue') },
        { path: 'points', name: 'points', component: () => import('@/views/UserPoints.vue') },
        { path: 'notices', name: 'notices', component: () => import('@/views/UserNotices.vue') },
        { path: 'help', name: 'help', component: () => import('@/views/UserHelp.vue') },
        { path: 'account', name: 'account', component: () => import('@/views/UserAccount.vue') },
        { path: 'admin', name: 'admin', component: () => import('@/views/AdminOverview.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/users', name: 'admin-users', component: () => import('@/views/AdminUsers.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/packages', name: 'admin-packages', component: () => import('@/views/AdminPackages.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/user-groups', name: 'admin-user-groups', component: () => import('@/views/AdminUserGroups.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/nodes', name: 'admin-nodes', component: () => import('@/views/AdminNodes.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/singbox', name: 'admin-singbox', component: () => import('@/views/AdminSingbox.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/certs', name: 'admin-certs', component: () => import('@/views/AdminCerts.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/orders', name: 'admin-orders', component: () => import('@/views/AdminOrders.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/servers', name: 'admin-servers', component: () => import('@/views/AdminServers.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/monitor', name: 'admin-monitor', component: () => import('@/views/AdminMonitor.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/monitor/:id', name: 'admin-monitor-detail', component: () => import('@/views/AdminMonitorDetail.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/settings', name: 'admin-settings', component: () => import('@/views/AdminSettings.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/reg-codes', name: 'admin-regcodes', component: () => import('@/views/AdminRegCodes.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/announcements', name: 'admin-announcements', component: () => import('@/views/AdminAnnouncements.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/manual-notifications', name: 'admin-manual-notifications', component: () => import('@/views/AdminManualNotifications.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/help', name: 'admin-help', component: () => import('@/views/AdminHelp.vue'), meta: { requiresAdmin: true } },
        { path: 'admin/update', name: 'admin-update', component: () => import('@/views/AdminUpdate.vue'), meta: { requiresAdmin: true } },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  // 等待 auth 初始化完成（首次加载时从 localStorage 恢复 token 并 fetchMe）
  if (!auth.loaded) {
    await auth.init()
  }

  // 已登录还落在登录页 → 直接进控制台（登录页只服务未登录/会话失效的人）
  if (to.name === 'login' && auth.isLoggedIn) {
    return { name: 'dashboard' }
  }

  const requiresAuth = to.matched.some(r => r.meta.requiresAuth)
  const requiresAdmin = to.matched.some(r => r.meta.requiresAdmin)

  if (requiresAuth && !auth.isLoggedIn) {
    // 品牌定制：未登录一律送独立登录页，并带 redirect 以便登录后回到原目的地。
    // 登录页是公开路由，守卫不会把它再拦回去，自指循环从结构上不可能出现。
    return {
      name: 'login',
      query: to.fullPath && to.fullPath !== '/' ? { redirect: to.fullPath } : undefined,
    }
  }
  if (requiresAdmin && !auth.isAdmin) {
    return { name: 'dashboard' }
  }
})

export default router
