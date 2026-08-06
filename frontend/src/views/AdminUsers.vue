<template>
  <div>
    <h2 class="page-title">用户管理</h2>
    <div class="page-toolbar">
      <n-input v-model:value="search" placeholder="搜索用户名/邮箱" style="width:260px;max-width:60%;" clearable />
      <n-checkbox v-model:checked="onlineOnly">只看在线 ({{ onlineCount }})</n-checkbox>
      <span class="spacer" />
      <n-button type="primary" @click="openCreate">创建用户</n-button>
    </div>
    <n-spin :show="loading">
      <div v-if="filtered.length" class="card-grid">
        <div v-for="u in filtered" :key="u.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ u.username }}</span>
            <n-tag v-if="u.online" type="success" size="tiny" bordered="false">在线</n-tag>
            <n-tag :type="u.status === 'banned' ? 'error' : 'success'" size="tiny" bordered="false">{{ u.status === 'banned' ? '封禁' : '正常' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">邮箱 <b>{{ u.email || '—' }}</b></span>
            <span class="kv">最后在线 <b>{{ timeAgo(u.last_online_at) }}</b></span>
            <span class="kv">积分 <b>{{ u.points }}</b></span>
            <span class="kv">流量 <b>{{ fmtBytes(u.used) }} / {{ fmtTotal(u.traffic_limit) }}</b></span>
            <span class="kv">到期 <b>{{ fmtDate(u.expiry_at) }}</b></span>
          </div>
          <div v-if="u.group_ids?.length" class="lc-meta">
            <span class="kv">用户组 <b>{{ groupNames(u.group_ids) }}</b></span>
          </div>
          <div class="lc-foot" style="flex-wrap:wrap;">
            <n-button size="tiny" @click="openEdit(u)">编辑</n-button>
            <n-button size="tiny" type="info" @click="openRecharge(u)">充值</n-button>
            <n-button size="tiny" type="warning" @click="openAssign(u)">分配</n-button>
            <n-button size="tiny" @click="openOrders(u)">订单</n-button>
            <!-- 用户端「重置节点凭据」默认关闭、且有 30 天冷却，文案让用户来找管理员，
                 这里就是那个入口——订阅泄露后彻底吊销旧链接的唯一办法。 -->
            <n-button size="tiny" type="warning" :loading="resettingCreds === u.id"
                      @click="handleResetCreds(u)">重置凭据</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(u)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" :description="onlineOnly ? '当前无在线用户' : '暂无用户'" style="padding:40px 0;" />
    </n-spin>

    <!-- 创建用户 -->
    <n-modal v-model:show="showCreate" preset="card" title="创建用户" style="max-width:400px;">
      <n-form label-placement="left" label-width="60">
        <n-form-item label="用户名"><n-input v-model:value="newUser.username" /></n-form-item>
        <n-form-item label="邮箱"><n-input v-model:value="newUser.email" /></n-form-item>
        <n-form-item label="密码"><n-input v-model:value="newUser.password" type="password" /></n-form-item>
        <n-form-item label="积分"><n-input-number v-model:value="newUser.points" :min="0" style="width:100%;" /></n-form-item>
        <n-form-item label="用户组">
          <n-select v-model:value="newUser.group_ids" :options="userGroupOptions" multiple clearable placeholder="留空 = 只能买公开套餐" />
        </n-form-item>
      </n-form>
      <n-button type="primary" block :loading="saving" @click="handleCreate">创建</n-button>
    </n-modal>

    <!-- 编辑用户 -->
    <n-modal v-model:show="showEdit" preset="card" title="编辑用户" style="max-width:500px;">
      <n-form v-if="editUser" label-placement="left" label-width="80">
        <n-form-item label="用户名"><n-input :value="editUser.username" disabled /></n-form-item>
        <n-form-item label="手动额度">
          <div style="width:100%;">
            <n-switch v-model:value="manualEnabled" />
            <div style="margin-top:4px;font-size:12px;color:var(--text-3);line-height:1.5;">
              管理员赠送的通用流量，作为一个独立计量的额度桶，作用于该用户「免费分组 + 已购套餐分组」的节点。需要指定具体节点分组请改用「分配计划」。
            </div>
          </div>
        </n-form-item>
        <template v-if="manualEnabled">
          <n-form-item label="不限流量"><n-switch v-model:value="unlimitedTraffic" /></n-form-item>
          <n-form-item v-if="!unlimitedTraffic" label="流量 (GB)"><n-input-number v-model:value="editTrafficGB" :min="0" style="width:100%;" /></n-form-item>
          <n-form-item label="到期时间"><n-input v-model:value="editExpiry" type="datetime-local" style="width:100%;" /></n-form-item>
        </template>
        <n-form-item label="用户组">
          <div style="width:100%;">
            <n-select v-model:value="editGroupIDs" :options="userGroupOptions" multiple clearable placeholder="留空 = 只能买公开套餐" />
            <div style="margin-top:4px;font-size:12px;color:var(--text-3);line-height:1.5;">
              决定该用户能买哪些专属套餐。移出用户组不影响其已购买的套餐。
            </div>
          </div>
        </n-form-item>
        <n-form-item label="封禁"><n-switch v-model:value="editBanned" /></n-form-item>
        <n-form-item label="重置密码"><n-input v-model:value="resetPw" type="password" placeholder="留空不重置" /></n-form-item>
        <n-form-item label="重置流量"><n-switch v-model:value="resetTraffic" /></n-form-item>
      </n-form>
      <n-button type="primary" block :loading="saving" @click="handleSave">保存</n-button>
    </n-modal>

    <!-- 积分充值 -->
    <n-modal v-model:show="showRecharge" preset="card" title="积分充值" style="max-width:400px;">
      <p style="font-size:13px;color:var(--text-2);margin-bottom:12px;">用户：{{ rechargeUser?.username }}</p>
      <n-form label-placement="left" label-width="60">
        <n-form-item label="积分"><n-input-number v-model:value="rechargeAmount" style="width:100%;" /></n-form-item>
        <n-form-item label="说明"><n-input v-model:value="rechargeNote" placeholder="正数充值，负数扣除" /></n-form-item>
      </n-form>
      <n-button type="primary" block :loading="saving" @click="handleRecharge">确认</n-button>
    </n-modal>

    <!-- 分配计划 -->
    <n-modal v-model:show="showAssign" preset="card" title="分配计划" style="max-width:440px;">
      <p style="font-size:13px;color:var(--text-2);margin-bottom:12px;">用户：{{ assignUser?.username }}</p>

      <!-- 当前套餐一览：便于判断分配后是排队还是立即生效 -->
      <div v-if="assignPlanList.length" style="margin-bottom:14px;">
        <div style="font-size:12px;color:var(--text-3);margin-bottom:6px;">当前套餐</div>
        <div v-for="p in assignPlanList" :key="p.id" style="display:flex;align-items:center;gap:8px;padding:6px 8px;background:var(--bg-soft);border-radius:8px;margin-bottom:6px;">
          <span style="flex:1;font-size:13px;font-weight:550;">{{ p.name }}</span>
          <n-tag :type="planStatusOf(p).type" size="tiny" bordered="false">{{ planStatusOf(p).label }}</n-tag>
          <span style="font-size:11px;color:var(--text-3);white-space:nowrap;">{{ planTimeOf(p) }}</span>
        </div>
      </div>
      <n-empty v-else-if="!loadingAssignPlans" description="该用户暂无套餐" size="small" style="padding:8px 0 14px;" />

      <n-form label-placement="left" label-width="60">
        <n-form-item label="套餐">
          <n-select v-model:value="assignPkgId" :options="pkgOptions" placeholder="选择套餐" />
        </n-form-item>
      </n-form>
      <n-alert v-if="assignWillQueue" type="info" size="small" style="margin-bottom:12px;">
        该用户已在使用此套餐，分配后将<b>排队</b>，在当前份用完或到期后自动启用。
      </n-alert>
      <n-button type="primary" block :loading="saving" @click="handleAssign">
        {{ assignWillQueue ? '分配（排队 · 不扣积分）' : '分配（不扣积分）' }}
      </n-button>
    </n-modal>

    <!-- 订单历史 -->
    <n-modal v-model:show="showOrders" preset="card" title="用户订单" style="max-width:700px;">
      <p style="font-size:13px;color:var(--text-2);margin-bottom:12px;">用户：{{ ordersUser?.username }}</p>
      <n-spin :show="loadingOrders">
        <div v-if="userOrders.length" class="card-grid compact">
          <div v-for="o in userOrders" :key="o.id" class="list-card">
            <div class="lc-head">
              <span class="lc-title">{{ o.name || '—' }}</span>
              <n-tag :type="o.status === 'success' ? 'success' : 'warning'" size="tiny" bordered="false">{{ o.status === 'success' ? '成功' : '已退款' }}</n-tag>
            </div>
            <div class="lc-meta">
              <span class="kv">积分 <b>{{ o.price_points }}</b></span>
              <span class="kv" v-if="o.status === 'refunded'">已退 <b style="color:var(--warn);">{{ o.refunded_points }}</b>
                <template v-if="o.refund_ratio > 0 && o.refund_ratio < 1">（{{ Math.round(o.refund_ratio * 100) }}%）</template>
              </span>
              <span class="kv">{{ fmtDateTime(o.created_at) }}</span>
            </div>
            <div class="lc-foot">
              <n-button v-if="o.status === 'success'" size="tiny" type="warning" @click="openRefund(o.id)">退款</n-button>
            </div>
          </div>
        </div>
        <n-empty v-else-if="!loadingOrders" description="暂无订单" style="padding:30px 0;" />
      </n-spin>
    </n-modal>

    <refund-dialog v-model:show="refundShow" :order-id="refundId" @done="reloadUserOrders" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted } from 'vue'
import {
  NSpin, NInput, NInputNumber, NButton, NModal, NForm, NFormItem,
  NSwitch, NTag, NSelect, NEmpty, NCheckbox, NAlert, useMessage, useDialog
} from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
import { fmtBytes, fmtTotal, fmtDate, fmtDateTime, timeAgo, toLocalDatetimeInput } from '@/utils/format'
import { planStatusMeta, planTimeText } from '@/utils/plan'
import RefundDialog from '@/components/RefundDialog.vue'

const message = useMessage()
const dialog = useDialog()
const users = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const search = ref('')
const onlineOnly = ref(false)

const onlineCount = computed(() => users.value.filter((u: any) => u.online).length)

const filtered = computed(() => {
  let list = users.value
  if (onlineOnly.value) list = list.filter((u: any) => u.online)
  if (!search.value) return list
  const q = search.value.toLowerCase()
  return list.filter((u: any) => u.username?.toLowerCase().includes(q) || u.email?.toLowerCase().includes(q))
})

// --- Create ---
const showCreate = ref(false)
const newUser = reactive({ username: '', email: '', password: '', points: 0, group_ids: [] as number[] })
function openCreate() { Object.assign(newUser, { username: '', email: '', password: '', points: 0, group_ids: [] }); showCreate.value = true }
async function handleCreate() {
  saving.value = true
  try { await apiPost('/api/admin/users', newUser); message.success('创建成功'); showCreate.value = false; await load() } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// --- Edit ---
const showEdit = ref(false)
const editUser = ref<any>(null)
const manualEnabled = ref(false)
const unlimitedTraffic = ref(false)
const editTrafficGB = ref(0)
const editExpiry = ref('')
const editBanned = ref(false)
const resetPw = ref('')
const resetTraffic = ref(false)
const editGroupIDs = ref<number[]>([])
async function openEdit(u: any) {
  editUser.value = { ...u }
  editBanned.value = u.status === 'banned'
  resetPw.value = ''; resetTraffic.value = false
  editGroupIDs.value = [...(u.group_ids || [])]
  // Prefill the manual-grant fields from the user's admin-grant bucket itself (not
  // the aggregate traffic_limit), so saving sets exactly that bucket — no double
  // counting against their purchased plans, and no accidental grant when there's none.
  manualEnabled.value = false
  unlimitedTraffic.value = false
  editTrafficGB.value = 0
  editExpiry.value = ''
  showEdit.value = true
  try {
    const plans = await apiList(`/api/admin/users/${u.id}/plans`)
    const grant = plans.find((p: any) => p.kind === 'plan' && p.package_id === 0)
    if (grant) {
      manualEnabled.value = true
      unlimitedTraffic.value = !grant.traffic_limit || grant.traffic_limit <= 0
      editTrafficGB.value = (grant.traffic_limit || 0) / (1024 * 1024 * 1024)
      editExpiry.value = toLocalDatetimeInput(grant.expiry_at)
    }
  } catch { /* leave the "no grant" defaults on error */ }
}
async function handleSave() {
  if (!editUser.value) return
  saving.value = true
  try {
    const body: any = {
      status: editBanned.value ? 'banned' : 'active',
      manual_enabled: manualEnabled.value,
      manual_traffic: manualEnabled.value && !unlimitedTraffic.value ? Math.round(editTrafficGB.value * 1024 * 1024 * 1024) : 0,
      manual_expiry: manualEnabled.value && editExpiry.value ? Math.floor(new Date(editExpiry.value).getTime() / 1000) : 0,
    }
    if (resetPw.value) body.password = resetPw.value
    if (resetTraffic.value) body.reset_traffic = true
    body.group_ids = editGroupIDs.value
    await apiPut(`/api/admin/users/${editUser.value.id}`, body)
    message.success('保存成功'); showEdit.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// --- Recharge ---
const showRecharge = ref(false)
const rechargeUser = ref<any>(null)
const rechargeAmount = ref(0)
const rechargeNote = ref('')
function openRecharge(u: any) { rechargeUser.value = u; rechargeAmount.value = 0; rechargeNote.value = ''; showRecharge.value = true }
async function handleRecharge() {
  saving.value = true
  try {
    await apiPost(`/api/admin/users/${rechargeUser.value.id}/points`, { amount: rechargeAmount.value, note: rechargeNote.value })
    message.success(rechargeAmount.value >= 0 ? '充值成功' : '扣除成功'); showRecharge.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// --- Assign plan ---
const showAssign = ref(false)
const assignUser = ref<any>(null)
const assignPkgId = ref<number | null>(null)
const pkgOptions = ref<any[]>([])
const assignPlans = ref<any[]>([])
const loadingAssignPlans = ref(false)
const assignPlanList = computed(() => assignPlans.value.filter((p: any) => p.kind === 'plan'))
// The chosen package already has a usable (active) bucket → the grant will queue.
const assignWillQueue = computed(() =>
  !!assignPkgId.value && assignPlanList.value.some((p: any) => p.package_id === assignPkgId.value && p.status === 'active'))
function openAssign(u: any) {
  assignUser.value = u; assignPkgId.value = null; showAssign.value = true
  loadPackages(); loadAssignPlans(u.id)
}
async function loadPackages() {
  try {
    const pkgs = await apiList<any>('/api/admin/packages')
    pkgOptions.value = pkgs.map((p: any) => ({ label: `${p.name} (${p.type})`, value: p.id }))
  } catch {}
}
async function loadAssignPlans(uid: number) {
  loadingAssignPlans.value = true
  try { assignPlans.value = await apiList(`/api/admin/users/${uid}/plans`) } catch {} finally { loadingAssignPlans.value = false }
}
function planStatusOf(p: any) { return planStatusMeta(p) }
function planTimeOf(p: any) { return planTimeText(p, fmtDate) }
async function handleAssign() {
  if (!assignPkgId.value) { message.warning('请选择套餐'); return }
  saving.value = true
  try {
    await apiPost(`/api/admin/users/${assignUser.value.id}/assign-plan`, { package_id: assignPkgId.value })
    message.success(assignWillQueue.value ? '已分配并加入队列' : '分配成功'); showAssign.value = false; await load()
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

// --- Orders ---
const showOrders = ref(false)
const ordersUser = ref<any>(null)
const userOrders = ref<any[]>([])
const loadingOrders = ref(false)
const refundShow = ref(false)
const refundId = ref<number | null>(null)
async function openOrders(u: any) {
  ordersUser.value = u; showOrders.value = true; loadingOrders.value = true
  try { userOrders.value = await apiList(`/api/admin/users/${u.id}/orders`) } catch {} finally { loadingOrders.value = false }
}
function openRefund(orderId: number) {
  refundId.value = orderId
  refundShow.value = true
}
async function reloadUserOrders() {
  if (!ordersUser.value) return
  // Refunding also changes the user's points/quota, so refresh both the order
  // list and the user table behind the modal.
  try { userOrders.value = await apiList(`/api/admin/users/${ordersUser.value.id}/orders`) } catch {}
  await load()
}

// --- Reset node credentials ---
// The operator half of「订阅泄露怎么办」. Swapping the user's subscription address
// only moves where the list is served; the node links already exported from the
// old address authenticate with the account's own credentials and keep working
// until those are rotated — which is what this does.
const resettingCreds = ref<number | null>(null)
function handleResetCreds(u: any) {
  dialog.error({
    title: '确认重置节点凭据',
    content: `为用户「${u.username}」重新生成所有节点凭据？从其旧订阅导出的节点将立即失效，`
      + '该用户需要重新导入订阅。凭据会马上推送到相关服务器，'
      + '推送会重启这些服务器上的 sing-box，其他用户的在线连接也会短暂中断。',
    positiveText: '重置', negativeText: '取消',
    onPositiveClick: async () => {
      resettingCreds.value = u.id
      try {
        await apiPost(`/api/admin/users/${u.id}/reset-node-creds`)
        message.success('已重置并推送，该用户需重新导入订阅')
      } catch (e: any) { message.error(e.message) }
      finally { resettingCreds.value = null }
    },
  })
}

// --- Delete ---
function handleDelete(u: any) {
  dialog.warning({
    title: '确认删除用户',
    content: `确定删除用户「${u.username}」？其订阅、套餐与设备将一并失效，此操作不可撤销。`,
    positiveText: '删除', negativeText: '取消',
    onPositiveClick: async () => {
      try { await apiDelete(`/api/admin/users/${u.id}`); message.success('已删除'); await load() }
      catch (e: any) { message.error(e.message) }
    },
  })
}

// --- User groups ---
const userGroups = ref<any[]>([])
const userGroupOptions = computed(() => userGroups.value.map(g => ({ label: g.name, value: g.id })))
function groupNames(ids?: number[]) {
  return (ids || []).map(id => userGroups.value.find(g => g.id === id)?.name).filter(Boolean).join('、')
}

async function load() {
  loading.value = true
  try {
    const [us, gs] = await Promise.all([
      apiList('/api/admin/users'),
      apiList('/api/admin/user-groups').catch(() => []),
    ])
    users.value = us; userGroups.value = gs
  } catch (e: any) { message.error('加载失败：' + (e?.message || '请稍后重试')) } finally { loading.value = false }
}
onMounted(load)
</script>
