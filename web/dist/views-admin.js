(function () {
const { app: A } = window.qz;

/* ===== ADMIN OVERVIEW ===== */
A.component('view-admin-overview', {
  data() { return { ov: null, traffic: [], top: null, dist: null, loading: true, showOnline: false }; },
  async mounted() {
    const [ov, traffic, top, dist] = await Promise.all([
      this.$api('/api/admin/stats/overview'), this.$api('/api/admin/stats/traffic?range=14d'),
      this.$api('/api/admin/stats/top'), this.$api('/api/admin/stats/distribution'),
    ]);
    this.ov = ov; this.traffic = traffic || []; this.top = top; this.dist = dist; this.loading = false;
  },
  computed: { revBars() { return (this.dist?.revenue || []).map(d => ({ date: d.date, up: d.b, down: d.a })); } },
  methods: {
    ago(ts) { const s = Math.max(0, Math.floor(Date.now() / 1000) - ts); if (s < 60) return s + ' 秒前'; if (s < 3600) return Math.floor(s / 60) + ' 分钟前'; return Math.floor(s / 3600) + ' 小时前'; },
  },
  template: `
  <div v-if="loading" class="center"><span class="spin"></span></div>
  <div v-else>
    <h1 class="page-title">数据总览</h1><div class="page-sub">面板运营概况</div>
    <div class="grid cols-4 mb">
      <div class="card stat"><div class="label">总会员</div><div class="value">{{ov.total_users}}</div><div class="sub">今日新增 {{ov.new_today}}</div></div>
      <div class="card stat" style="cursor:pointer" @click="showOnline=!showOnline" title="点击查看在线用户"><div class="label">当前在线</div><div class="value">{{ov.online}}</div><div class="sub">已开通 {{ov.active_users}} ›</div></div>
      <div class="card stat"><div class="label">累计流量</div><div class="value">{{$fmt(ov.total_traffic)}}</div><div class="sub">全站已用</div></div>
      <div class="card stat"><div class="label">积分发行</div><div class="value">{{ov.points_issued}}</div><div class="sub">在售商品 {{ov.packages_on}}</div></div>
    </div>
    <div v-if="showOnline" class="card mb">
      <div class="card-h"><h3>在线用户（近 5 分钟有流量）</h3><span class="small dim">{{(ov.online_users||[]).length}} 人</span></div>
      <div v-if="!(ov.online_users||[]).length" class="empty small">当前无人在线</div>
      <div v-for="(u,i) in (ov.online_users||[])" :key="i" class="between" style="padding:6px 0;border-bottom:1px solid var(--border-soft)"><span class="muted"><i style="display:inline-block;width:7px;height:7px;border-radius:50%;background:#5cb784;margin-right:7px"></i>{{u.name}}</span><span class="small dim">{{ago(u.value)}}</span></div>
    </div>
    <div class="card mb">
      <div class="card-h"><h3>全站流量趋势（近 14 天）</h3></div>
      <bars :data="traffic"/>
    </div>
    <div class="grid cols-2 mb">
      <div class="card"><div class="card-h"><h3>流量排行</h3></div>
        <div v-if="!top.traffic.length" class="empty small">暂无数据</div>
        <div v-for="(t,i) in top.traffic" :key="i" class="between" style="padding:7px 0;border-bottom:1px solid var(--border-soft)"><span class="muted">{{i+1}}. {{t.name}}</span><b>{{$fmt(t.value)}}</b></div>
      </div>
      <div class="card"><div class="card-h"><h3>消费排行</h3></div>
        <div v-if="!top.spend.length" class="empty small">暂无数据</div>
        <div v-for="(t,i) in top.spend" :key="i" class="between" style="padding:7px 0;border-bottom:1px solid var(--border-soft)"><span class="muted">{{i+1}}. {{t.name}}</span><b>{{t.value}} 积分</b></div>
      </div>
    </div>
    <div class="grid cols-2">
      <div class="card"><div class="card-h"><h3>用户分布</h3></div>
        <div class="grid cols-2" style="gap:10px">
          <div class="stat"><div class="label">活跃</div><div class="value" style="font-size:20px">{{dist.distribution.status_active||0}}</div></div>
          <div class="stat"><div class="label">已封禁</div><div class="value" style="font-size:20px">{{dist.distribution.status_banned||0}}</div></div>
          <div class="stat"><div class="label">7天内到期</div><div class="value" style="font-size:20px">{{dist.distribution.expire_7d||0}}</div></div>
          <div class="stat"><div class="label">已过期</div><div class="value" style="font-size:20px">{{dist.distribution.expired||0}}</div></div>
        </div>
      </div>
      <div class="card"><div class="card-h"><h3>积分收支（近 30 天）</h3></div>
        <bars :data="revBars"/>
        <div class="flex small dim mt" style="gap:16px"><span><i style="display:inline-block;width:9px;height:9px;border-radius:3px;background:#bcd2c1"></i> 发行</span><span><i style="display:inline-block;width:9px;height:9px;border-radius:3px;background:var(--accent)"></i> 消费</span></div>
      </div>
    </div>
  </div>`,
});

/* ===== ADMIN USERS ===== */
A.component('view-admin-users', {
  data() { return { users: [], q: '', rc: null, amount: 0, note: '', nu: null, busy: false, ed: null, pkgs: [], ap: null, oh: null, refBusy: 0 }; },
  async mounted() { await this.load(); this.pkgs = await this.$api('/api/admin/packages') || []; },
  methods: {
    async load() { this.users = await this.$api('/api/admin/users?q=' + encodeURIComponent(this.q)) || []; },
    openAssign(u) { const first = (this.pkgs.find(p => p.enabled) || this.pkgs[0]); this.ap = { id: u.id, username: u.username, package_id: first ? first.id : 0 }; },
    async assignPlan() {
      if (!this.ap.package_id) { this.$toast('请选择套餐', true); return; }
      this.busy = true;
      try { await this.$api('/api/admin/users/' + this.ap.id + '/assign-plan', { method: 'POST', body: { package_id: Number(this.ap.package_id) } }); this.$toast('已为「' + this.ap.username + '」开通'); this.ap = null; await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
    async openOrders(u) {
      this.oh = { id: u.id, username: u.username, orders: [], plans: [], loading: true };
      this.oh.plans = await this.$api('/api/admin/users/' + u.id + '/plans') || [];
      this.oh.orders = await this.$api('/api/admin/users/' + u.id + '/orders') || [];
      this.oh.loading = false;
    },
    planText(p) {
      const limit = p.remaining < 0 ? '不限' : this.$total(p.traffic_limit);
      const t = '已用 ' + this.$fmt(p.used) + ' / ' + limit;
      if (p.kind === 'pool') return t;
      const exp = p.expiry_at ? (this.$days(p.expiry_at) + ' 天后到期') : '永久';
      const st = p.status === 'active' ? '生效中' : (p.status === 'expired' ? '已到期' : '已用尽');
      return t + ' · ' + exp + ' · ' + st;
    },
    ostatus(o) { return o.status === 'refunded' ? { t: '已退款', cls: 'amber' } : { t: '成功', cls: 'green' }; },
    async refundOrder(o) {
      if (!confirm('确定退款订单 #' + o.id + '？\n将退回 ' + o.price_points + ' 积分，并撤销本次开通的流量/到期，订单标记为「已退款」（记录保留）。')) return;
      this.refBusy = o.id;
      try { const r = await this.$api('/api/admin/orders/' + o.id + '/refund', { method: 'POST' }); this.$toast('已退款，余额 ' + r.points); await this.openOrders({ id: this.oh.id, username: this.oh.username }); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.refBusy = 0;
    },
    toLocal(u) { if (!u) return ''; const d = new Date(u * 1000), p = n => String(n).padStart(2, '0'); return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + 'T' + p(d.getHours()) + ':' + p(d.getMinutes()); },
    fromLocal(s) { return s ? Math.floor(new Date(s).getTime() / 1000) : 0; },
    edit(u) { this.ed = { id: u.id, username: u.username, unlimited: u.traffic_limit === 0, traffic_gb: u.traffic_limit ? u.traffic_limit / 1073741824 : 10, expiry_str: this.toLocal(u.expiry_at), status: u.status, password: '', reset: false }; },
    async saveEdit() {
      const e = this.ed;
      const body = { traffic_limit: e.unlimited ? 0 : Math.round(Number(e.traffic_gb) * 1073741824), expiry_at: this.fromLocal(e.expiry_str), status: e.status, password: e.password, reset_traffic: e.reset };
      try { await this.$api('/api/admin/users/' + e.id, { method: 'PUT', body }); this.$toast('已保存'); this.ed = null; this.load(); }
      catch (err) { this.$toast(err.message, true); }
    },
    newUser() { this.nu = { username: '', email: '', password: '', points: 0 }; },
    async createUser() {
      const n = this.nu;
      if (!/^[a-zA-Z0-9_]{3,32}$/.test(n.username)) { this.$toast('用户名需 3-32 位字母/数字/下划线', true); return; }
      if ((n.password || '').length < 6) { this.$toast('密码至少 6 位', true); return; }
      this.busy = true;
      try { await this.$api('/api/admin/users', { method: 'POST', body: { username: n.username, email: n.email, password: n.password, points: Number(n.points) || 0 } }); this.$toast('已创建用户「' + n.username + '」'); this.nu = null; await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
    async recharge() { try { const r = await this.$api('/api/admin/users/' + this.rc.id + '/points', { method: 'POST', body: { amount: Number(this.amount), note: this.note } }); this.$toast('已更新，余额 ' + r.balance); this.rc = null; this.amount = 0; this.note = ''; this.load(); } catch (e) { this.$toast(e.message, true); } },
    async del(u) { if (!confirm('删除用户 ' + u.username + '？将同时删除其节点账号。')) return; await this.$api('/api/admin/users/' + u.id, { method: 'DELETE' }); this.$toast('已删除'); this.load(); },
    ustatus(u) {
      if (u.status === 'banned') return { t: '封禁', cls: 'red' };
      if (u.expiry_at && u.expiry_at * 1000 < Date.now()) return { t: '已过期', cls: 'amber' };
      if (u.traffic_limit > 0 && u.used >= u.traffic_limit) return { t: '流量耗尽', cls: 'amber' };
      return { t: '正常', cls: 'green' };
    },
  },
  template: `
  <div>
    <h1 class="page-title">用户管理</h1><div class="page-sub">充值积分、查看与管理会员</div>
    <div class="card">
      <div class="card-h"><h3>会员列表</h3><div class="flex"><input class="input" style="width:180px" v-model="q" @keyup.enter="load" placeholder="搜索用户名/邮箱"><button class="btn sm" @click="load">搜索</button><button class="btn primary sm" @click="newUser">+ 新建用户</button></div></div>
      <table class="table"><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>积分</th><th>流量</th><th>到期</th><th></th></tr></thead>
      <tbody><tr v-for="u in users" :key="u.id">
        <td><b>{{u.username}}</b><div class="small dim">{{u.email||'—'}}<span v-if="u.email" class="badge xs" :class="u.email_verified?'green':'amber'" style="margin-left:6px">{{u.email_verified?'已验证':'未验证'}}</span></div></td>
        <td><span class="badge" :class="u.role==='admin'?'amber':''">{{u.role==='admin'?'管理员':'会员'}}</span></td>
        <td><span class="badge" :class="ustatus(u).cls">{{ustatus(u).t}}</span></td>
        <td>{{u.points}}</td>
        <td class="small">{{$fmt(u.used)}} / {{$total(u.traffic_limit)}}</td>
        <td class="small dim">{{$date(u.expiry_at)}}</td>
        <td><div class="flex"><button class="btn sm" @click="rc=u">充值</button><button v-if="u.role!=='admin'" class="btn sm" @click="openAssign(u)">开通套餐</button><button v-if="u.role!=='admin'" class="btn sm" @click="openOrders(u)">消费记录</button><button v-if="u.role!=='admin'" class="btn sm" @click="edit(u)">编辑</button><button v-if="u.role!=='admin'" class="btn sm danger" @click="del(u)">删除</button></div></td>
      </tr></tbody></table>
    </div>
    <modal v-if="ed" :title="'编辑 '+ed.username" @close="ed=null">
      <div class="field"><label>流量额度</label>
        <label class="flex" style="margin-bottom:8px"><input type="checkbox" v-model="ed.unlimited"> <span>不限流量</span></label>
        <input v-if="!ed.unlimited" class="input" type="number" step="0.1" v-model="ed.traffic_gb" placeholder="GB">
      </div>
      <div class="field"><label>到期时间（留空=永久）</label><input class="input" type="datetime-local" v-model="ed.expiry_str"></div>
      <div class="field"><label>状态</label><select v-model="ed.status"><option value="active">正常</option><option value="banned">封禁（停用其节点）</option></select></div>
      <div class="field"><label>重置密码（留空=不改）</label><input class="input" type="password" v-model="ed.password" placeholder="至少 6 位，改后该用户需重新登录"></div>
      <label class="flex" style="margin:6px 0 16px"><input type="checkbox" v-model="ed.reset"> <span>同时把"已用流量"清零</span></label>
      <button class="btn primary block" @click="saveEdit">保存</button>
    </modal>
    <modal v-if="nu" title="新建用户" @close="nu=null">
      <div class="field"><label>用户名</label><input class="input" v-model="nu.username" placeholder="3-32 位字母/数字/下划线"></div>
      <div class="field"><label>邮箱（可选）</label><input class="input" v-model="nu.email" placeholder="可留空"></div>
      <div class="field"><label>密码</label><input class="input" type="password" v-model="nu.password" placeholder="至少 6 位"></div>
      <div class="field"><label>初始积分（可选）</label><input class="input" type="number" v-model="nu.points"></div>
      <div class="small dim mb">将按默认额度开通节点（流量/设备/有效期见「设置」），账号免邮箱验证直接可用。</div>
      <button class="btn primary block" :disabled="busy" @click="createUser"><span v-if="busy" class="spin"></span><span v-else>创建并开通</span></button>
    </modal>
    <modal v-if="rc" :title="'为 '+rc.username+' 充值积分'" @close="rc=null">
      <div class="field"><label>积分变动（正充值，负扣减）</label><input class="input" type="number" v-model="amount"></div>
      <div class="field"><label>备注</label><input class="input" v-model="note" placeholder="可选"></div>
      <button class="btn primary block" @click="recharge">确认</button>
    </modal>
    <modal v-if="ap" :title="'为 '+ap.username+' 开通套餐'" @close="ap=null">
      <div class="field"><label>选择套餐</label>
        <select v-model="ap.package_id">
          <option v-for="p in pkgs" :key="p.id" :value="p.id">{{p.name}}（{{p.type==='plan'?'套餐':'流量包'}}{{p.traffic_bytes?' · '+$gb(p.traffic_bytes)+'G':''}}{{p.type==='plan'&&p.duration_days?' · '+p.duration_days+'天':''}}）{{p.enabled?'':' [已下架]'}}</option>
        </select>
      </div>
      <div class="small dim mb">直接开通，<b>不扣积分</b>。套餐：活跃用户叠加流量并延长到期；已过期则重置流量、从今天起算。会立即同步到节点。</div>
      <button class="btn primary block" :disabled="busy" @click="assignPlan"><span v-if="busy" class="spin"></span><span v-else>确认开通</span></button>
    </modal>
    <modal v-if="oh" :title="oh.username+' 的消费记录'" @close="oh=null">
      <div v-if="oh.loading" class="empty">加载中…</div>
      <template v-else>
      <div v-if="oh.plans && oh.plans.length" class="card mb" style="background:var(--bg-soft)">
        <div class="card-h"><h3 style="font-size:15px">当前套餐（独立计费）</h3><span class="small dim">{{oh.plans.length}} 项</span></div>
        <div v-for="p in oh.plans" :key="p.id" class="between" style="padding:5px 0;border-bottom:1px solid var(--border-soft)">
          <span style="white-space:nowrap"><b>{{p.name}}</b> <span v-if="p.kind==='pool'" class="badge blue">通用</span></span>
          <span class="small dim">{{planText(p)}}</span>
        </div>
      </div>
      <div v-if="!oh.orders.length" class="empty">该用户暂无消费记录</div>
      <table v-else class="table"><thead><tr><th>#</th><th>商品</th><th>积分</th><th>状态</th><th>时间</th><th></th></tr></thead>
      <tbody><tr v-for="o in oh.orders" :key="o.id">
        <td class="small dim">{{o.id}}</td>
        <td style="white-space:nowrap">{{o.name||'—'}}<span class="small dim"> · {{o.type==='plan'?'套餐':(o.type==='traffic'?'流量包':o.type)}}</span></td>
        <td>{{o.price_points}}</td>
        <td style="white-space:nowrap"><span class="badge" :class="ostatus(o).cls">{{ostatus(o).t}}</span></td>
        <td class="small dim" style="white-space:nowrap">{{$dt(o.created_at)}}</td>
        <td><button v-if="o.status!=='refunded'" class="btn sm danger" :disabled="refBusy===o.id" @click="refundOrder(o)"><span v-if="refBusy===o.id" class="spin"></span><span v-else>退款</span></button></td>
      </tr></tbody></table>
      </template>
    </modal>
  </div>`,
});

/* ===== ADMIN PACKAGES ===== */
A.component('view-admin-packages', {
  data() { return { pkgs: [], groups: [], editing: null }; },
  async mounted() { await this.load(); this.groups = await this.$api('/api/admin/node-groups') || []; },
  methods: {
    async load() { this.pkgs = await this.$api('/api/admin/packages') || []; },
    blank() { return { type: 'traffic', name: '', description: '', price_points: 100, gb: 10, device_add: 0, duration_days: 30, stock: -1, enabled: true, group_ids: [] }; },
    edit(p) { this.editing = p ? { ...p, gb: this.$gb(p.traffic_bytes), group_ids: p.group_ids || [] } : this.blank(); },
    toggleGroup(id) { const i = this.editing.group_ids.indexOf(id); if (i < 0) this.editing.group_ids.push(id); else this.editing.group_ids.splice(i, 1); },
    async save() {
      const e = this.editing; const body = { ...e, traffic_bytes: Math.round(Number(e.gb) * 1073741824), price_points: Number(e.price_points), device_add: Number(e.device_add), duration_days: Number(e.duration_days), stock: Number(e.stock) };
      try { if (e.id) await this.$api('/api/admin/packages/' + e.id, { method: 'PUT', body }); else await this.$api('/api/admin/packages', { method: 'POST', body }); this.editing = null; this.$toast('已保存'); this.load(); } catch (err) { this.$toast(err.message, true); }
    },
    async del(p) {
      if (!confirm('删除商品「' + p.name + '」？')) return;
      try { await this.$api('/api/admin/packages/' + p.id, { method: 'DELETE' }); this.$toast('已删除'); this.load(); }
      catch (e) { this.$toast(e.message, true); }
    },
    async retire(p) {
      if (!confirm('下架「' + p.name + '」？下架后商城将不可购买。')) return;
      if (p.subscribers > 0) {
        if (!confirm('⚠️ 该套餐当前有 ' + p.subscribers + ' 位用户订阅。\n下架将：退还他们的积分、撤销本次开通的流量/到期、清空其套餐（节点立即停用），订单标记为「已退款」（记录保留）。\n此操作不可撤销，确认继续？')) return;
      }
      try { const r = await this.$api('/api/admin/packages/' + p.id + '/retire', { method: 'POST' }); this.$toast('已下架' + (r.subscribers ? '；退款 ' + r.refunded + ' 人、清空 ' + r.cleared + ' 人套餐' : '')); this.load(); }
      catch (e) { this.$toast(e.message, true); }
    },
    async enable(p) {
      try { await this.$api('/api/admin/packages/' + p.id + '/enable', { method: 'POST' }); this.$toast('已上架'); this.load(); }
      catch (e) { this.$toast(e.message, true); }
    },
    gname(id) { const g = this.groups.find(x => x.id === id); return g ? g.name : id; },
  },
  template: `
  <div>
    <div class="between mb"><div><h1 class="page-title">商品管理</h1><div class="muted small">流量包 / 订阅套餐 / 设备扩容</div></div><button class="btn primary" @click="edit(null)">+ 新建商品</button></div>
    <div class="card">
      <div v-if="!pkgs.length" class="empty">还没有商品，点击右上角新建</div>
      <table v-else class="table"><thead><tr><th>名称</th><th>类型</th><th>价格</th><th>内容</th><th>分组</th><th>订阅</th><th>状态</th><th></th></tr></thead>
      <tbody><tr v-for="p in pkgs" :key="p.id">
        <td><b>{{p.name}}</b></td>
        <td><span class="badge">{{ {traffic:'流量包',plan:'套餐',device:'设备包'}[p.type] }}</span></td>
        <td>{{p.price_points}} 积分</td>
        <td class="small">{{p.traffic_bytes?'+'+$fmt(p.traffic_bytes):''}}{{p.device_add?' +'+p.device_add+'设备':''}}{{p.duration_days?' '+p.duration_days+'天':''}}</td>
        <td class="small dim">{{p.type==='plan'?(p.group_ids||[]).map(gname).join(', ')||'—':'—'}}</td>
        <td>{{p.subscribers||0}}</td>
        <td><span class="badge" :class="p.enabled?'green':''">{{p.enabled?'上架':'下架'}}</span></td>
        <td><div class="flex"><button class="btn sm" @click="edit(p)">编辑</button><button v-if="p.enabled" class="btn sm danger" @click="retire(p)">下架</button><button v-else class="btn sm" @click="enable(p)">上架</button><button class="btn sm danger" @click="del(p)">删</button></div></td>
      </tr></tbody></table>
    </div>
    <modal v-if="editing" :title="editing.id?'编辑商品':'新建商品'" @close="editing=null">
      <div class="field"><label>类型</label><select v-model="editing.type"><option value="traffic">流量包</option><option value="plan">订阅套餐</option></select></div>
      <div class="field"><label>名称</label><input class="input" v-model="editing.name"></div>
      <div class="field"><label>描述</label><input class="input" v-model="editing.description" placeholder="面向用户的说明"></div>
      <div class="row">
        <div class="field"><label>价格（积分）</label><input class="input" type="number" v-model="editing.price_points"></div>
        <div class="field"><label>库存（-1不限）</label><input class="input" type="number" v-model="editing.stock"></div>
      </div>
      <div class="row">
        <div class="field"><label>流量 (GB)</label><input class="input" type="number" v-model="editing.gb"></div>
        <div class="field" v-if="editing.type==='plan'"><label>有效天数</label><input class="input" type="number" v-model="editing.duration_days"></div>
      </div>
      <div class="field" v-if="editing.type==='plan'"><label>可用节点分组（套餐授予）</label>
        <div class="flex wrap"><span v-for="g in groups" class="chip" :class="{on:editing.group_ids.includes(g.id)}" @click="toggleGroup(g.id)">{{g.name}}</span><span v-if="!groups.length" class="small dim">先到「节点 & 分组」创建分组</span></div>
      </div>
      <label class="flex" style="margin:6px 0 16px"><input type="checkbox" v-model="editing.enabled"> <span>上架销售</span></label>
      <button class="btn primary block" @click="save">保存</button>
    </modal>
  </div>`,
});

/* ===== ADMIN NODES & GROUPS ===== */
A.component('view-admin-nodes', {
  data() { return { tab: 'groups', groups: [], nodes: [], sources: [], inbounds: [], newGroup: '', editGroup: null, nodeForm: null, importForm: null, srcForm: null }; },
  async mounted() { this.reload(); this.loadInbounds(); },
  methods: {
    async reload() { try { this.groups = await this.$api('/api/admin/node-groups') || []; this.nodes = await this.$api('/api/admin/nodes') || []; this.sources = await this.$api('/api/admin/node-sources') || []; } catch (e) { this.$toast(e.message, true); } },
    async loadInbounds() { try { this.inbounds = await this.$api('/api/admin/inbounds') || []; } catch (e) { this.$toast('读取入站失败: ' + e.message, true); } },
    async addGroup() {
      const name = (this.newGroup || '').trim();
      if (!name) { this.$toast('请先输入分组名称', true); return; }
      try { await this.$api('/api/admin/node-groups', { method: 'POST', body: { name } }); this.newGroup = ''; this.$toast('已创建分组「' + name + '」'); await this.reload(); }
      catch (e) { this.$toast(e.message, true); }
    },
    async delGroup(g) { if (!confirm('删除分组「' + g.name + '」？')) return; await this.$api('/api/admin/node-groups/' + g.id, { method: 'DELETE' }); this.reload(); },
    startEditGroup(g) { this.editGroup = { id: g.id, name: g.name, description: g.description || '', sort_order: g.sort_order || 0 }; },
    cancelEditGroup() { this.editGroup = null; },
    async saveGroup() {
      const e = this.editGroup; if (!e) return;
      const name = (e.name || '').trim();
      if (!name) { this.$toast('分组名称不能为空', true); return; }
      try { await this.$api('/api/admin/node-groups/' + e.id, { method: 'PUT', body: { name, description: (e.description || '').trim(), sort_order: e.sort_order } }); this.editGroup = null; this.$toast('分组已更新'); await this.reload(); }
      catch (err) { this.$toast(err.message, true); }
    },
    gname(id) { const g = this.groups.find(x => x.id === id); return g ? g.name : id; },
    newNode() { this.nodeForm = { type: 'self_built', name: '', inbound_tag: '', share_link: '', enabled: true, group_ids: [] }; },
    editNode(n) { this.nodeForm = { id: n.id, type: n.type, name: n.name || '', inbound_tag: n.inbound_tag || '', share_link: n.share_link || '', enabled: n.enabled, group_ids: (n.group_ids || []).slice() }; },
    toggleG(id) { const a = this.nodeForm.group_ids, i = a.indexOf(id); if (i < 0) a.push(id); else a.splice(i, 1); },
    async saveNode() {
      const f = this.nodeForm;
      if (f.type === 'self_built' && f.name === '') { const ib = this.inbounds.find(x => x.tag === f.inbound_tag); if (ib) f.name = ib.tag; }
      const u = '/api/admin/nodes' + (f.id ? '/' + f.id : '');
      try { await this.$api(u, { method: f.id ? 'PUT' : 'POST', body: f }); this.nodeForm = null; this.$toast('已保存'); this.reload(); } catch (e) { this.$toast(e.message, true); }
    },
    async delNode(n) { await this.$api('/api/admin/nodes/' + n.id, { method: 'DELETE' }); this.reload(); },
    openImport() { this.importForm = { links: '', group_ids: [] }; },
    toggleIG(id) { const a = this.importForm.group_ids, i = a.indexOf(id); if (i < 0) a.push(id); else a.splice(i, 1); },
    async doImport() { const r = await this.$api('/api/admin/nodes/import', { method: 'POST', body: this.importForm }); this.$toast('导入 ' + r.imported + ' 个节点'); this.importForm = null; this.reload(); },
    newSrc() { this.srcForm = { name: '', url: '', type: 'base64', enabled: true, group_ids: [] }; },
    editSrc(s) { this.srcForm = { id: s.id, name: s.name, url: s.url, type: s.type || 'base64', enabled: !!s.enabled, group_ids: (s.group_ids || []).slice() }; },
    toggleSG(id) { const a = this.srcForm.group_ids, i = a.indexOf(id); if (i < 0) a.push(id); else a.splice(i, 1); },
    async saveSrc() {
      const f = this.srcForm;
      const u = '/api/admin/node-sources' + (f.id ? '/' + f.id : '');
      try { await this.$api(u, { method: f.id ? 'PUT' : 'POST', body: f }); this.srcForm = null; this.$toast('已保存'); this.reload(); } catch (e) { this.$toast(e.message, true); }
    },
    async fetchSrc(s) { this.$toast('抓取中…'); try { const r = await this.$api('/api/admin/node-sources/' + s.id + '/fetch', { method: 'POST', body: {} }); this.$toast('抓取 ' + r.imported + ' 个节点'); this.reload(); } catch (e) { this.$toast(e.message, true); } },
    async delSrc(s) { if (!confirm('删除订阅源？其节点也会移除。')) return; await this.$api('/api/admin/node-sources/' + s.id, { method: 'DELETE' }); this.reload(); },
  },
  template: `
  <div>
    <h1 class="page-title">节点分组</h1><div class="page-sub">管理分组、节点与订阅源</div>
    <div class="flex wrap mb"><span class="chip" :class="{on:tab==='groups'}" @click="tab='groups'">分组</span><span class="chip" :class="{on:tab==='nodes'}" @click="tab='nodes'">节点</span><span class="chip" :class="{on:tab==='sources'}" @click="tab='sources'">订阅源</span></div>

    <div v-if="tab==='groups'" class="card">
      <div class="card-h"><h3>节点分组</h3></div>
      <div class="flex mb"><input class="input" style="max-width:240px" v-model="newGroup" @keyup.enter="addGroup" placeholder="分组名，如 高速/美国"><button class="btn primary sm" @click="addGroup">+ 新建分组</button></div>
      <div v-if="!groups.length" class="empty small">还没有分组</div>
      <table v-else class="table"><thead><tr><th>分组</th><th>节点数</th><th></th></tr></thead>
      <tbody><tr v-for="g in groups" :key="g.id">
        <template v-if="editGroup && editGroup.id===g.id">
          <td><input class="input sm" style="max-width:160px" v-model="editGroup.name" @keyup.enter="saveGroup" placeholder="分组名"> <input class="input sm" style="max-width:200px" v-model="editGroup.description" @keyup.enter="saveGroup" placeholder="描述（可选）"></td>
          <td>{{nodes.filter(n=>(n.group_ids||[]).includes(g.id)).length}}</td>
          <td><button class="btn sm primary" @click="saveGroup">保存</button> <button class="btn sm" @click="cancelEditGroup">取消</button></td>
        </template>
        <template v-else>
          <td><b>{{g.name}}</b> <span class="small dim">{{g.description}}</span></td>
          <td>{{nodes.filter(n=>(n.group_ids||[]).includes(g.id)).length}}</td>
          <td><button class="btn sm" @click="startEditGroup(g)">编辑</button> <button class="btn sm danger" @click="delGroup(g)">删除</button></td>
        </template>
      </tr></tbody></table>
    </div>

    <div v-if="tab==='nodes'" class="card">
      <div class="card-h"><h3>节点</h3><div class="flex"><button class="btn sm" @click="openImport">批量导入</button><button class="btn primary sm" @click="newNode">+ 新建节点</button></div></div>
      <div v-if="!nodes.length" class="empty small">还没有节点</div>
      <table v-else class="table"><thead><tr><th>名称</th><th>类型</th><th>标识</th><th>分组</th><th>状态</th><th></th></tr></thead>
      <tbody><tr v-for="n in nodes" :key="n.id">
        <td><b>{{n.name}}</b></td><td><span class="badge" :class="n.type==='self_built'?'green':'blue'">{{n.type==='self_built'?'自建':'外部'}}</span></td>
        <td class="small dim" style="max-width:240px;overflow:hidden;text-overflow:ellipsis">{{n.type==='self_built'?n.inbound_tag:n.share_link}}</td>
        <td class="small dim">{{(n.group_ids||[]).map(gname).join(', ')||'—'}}</td>
        <td><span class="badge" :class="n.enabled?'green':''">{{n.enabled?'启用':'停用'}}</span></td>
        <td><div class="flex"><button class="btn sm" @click="editNode(n)">编辑</button><button class="btn sm danger" @click="delNode(n)">删</button></div></td>
      </tr></tbody></table>
    </div>

    <div v-if="tab==='sources'" class="card">
      <div class="card-h"><h3>机场订阅源</h3><button class="btn primary sm" @click="newSrc">+ 添加订阅源</button></div>
      <div v-if="!sources.length" class="empty small">还没有订阅源</div>
      <table v-else class="table"><thead><tr><th>名称</th><th>地址</th><th>分组</th><th>节点数</th><th>最近抓取</th><th></th></tr></thead>
      <tbody><tr v-for="s in sources" :key="s.id"><td><b>{{s.name}}</b></td><td class="small dim" style="max-width:220px;overflow:hidden;text-overflow:ellipsis">{{s.url}}</td><td class="small dim">{{(s.group_ids||[]).map(gname).join(', ')||'—'}}</td><td>{{s.last_count}}</td><td class="small dim">{{s.last_fetched?$date(s.last_fetched):'未抓取'}}<div v-if="s.last_error" class="small" style="color:var(--danger)">{{s.last_error}}</div></td><td><div class="flex"><button class="btn sm" @click="fetchSrc(s)">抓取</button><button class="btn sm" @click="editSrc(s)">编辑</button><button class="btn sm danger" @click="delSrc(s)">删</button></div></td></tr></tbody></table>
    </div>

    <modal v-if="nodeForm" :title="nodeForm.id?'编辑节点':'新建节点'" @close="nodeForm=null">
      <div class="field"><label>类型</label><select v-model="nodeForm.type"><option value="self_built">自建（绑定入站）</option><option value="external">外部（分享链接）</option></select></div>
      <div class="field" v-if="nodeForm.type==='self_built'"><label>对应入站 <a class="small" style="cursor:pointer" @click="loadInbounds">↻刷新</a></label><select v-model="nodeForm.inbound_tag"><option value="">选择…</option><option v-for="ib in inbounds" :value="ib.tag">{{ib.tag}}（{{ib.type}}）</option></select><div v-if="!inbounds.length" class="small" style="color:var(--danger);margin-top:6px">还没有入站：请先到「节点配置」页创建入站，再点↻刷新</div></div>
      <template v-else>
        <div class="field"><label>分享链接</label><textarea v-model="nodeForm.share_link" placeholder="vless:// 或 trojan:// 等"></textarea></div>
        <div class="field"><label>名称</label><input class="input" v-model="nodeForm.name" placeholder="可留空，自动取链接备注"></div>
      </template>
      <div class="field"><label>所属分组</label><div class="flex wrap"><span v-for="g in groups" class="chip" :class="{on:nodeForm.group_ids.includes(g.id)}" @click="toggleG(g.id)">{{g.name}}</span></div></div>
      <button class="btn primary block" @click="saveNode">保存</button>
    </modal>

    <modal v-if="importForm" title="批量导入外部节点" @close="importForm=null">
      <div class="field"><label>粘贴分享链接（每行一个，或 base64 订阅内容）</label><textarea style="min-height:140px" v-model="importForm.links"></textarea></div>
      <div class="field"><label>加入分组</label><div class="flex wrap"><span v-for="g in groups" class="chip" :class="{on:importForm.group_ids.includes(g.id)}" @click="toggleIG(g.id)">{{g.name}}</span></div></div>
      <button class="btn primary block" @click="doImport">导入</button>
    </modal>

    <modal v-if="srcForm" :title="srcForm.id?'编辑订阅源':'添加订阅源'" @close="srcForm=null">
      <div class="field"><label>名称</label><input class="input" v-model="srcForm.name"></div>
      <div class="field"><label>订阅地址</label><input class="input" v-model="srcForm.url" placeholder="https://...">
        <div class="small dim" style="margin-top:6px">仅支持 Base64 / 纯链接列表订阅（vless:// trojan:// 等）。Clash YAML、sing-box JSON 格式暂不能解析为节点。</div></div>
      <div class="field"><label>导入到分组</label><div v-if="!groups.length" class="small dim">还没有分组，请先到「分组」页创建</div><div v-else class="flex wrap"><span v-for="g in groups" class="chip" :class="{on:srcForm.group_ids.includes(g.id)}" @click="toggleSG(g.id)">{{g.name}}</span></div><div class="small dim" style="margin-top:6px">抓取到的节点会自动加入选中的分组（含定时自动同步）；用户的套餐绑定该分组即可使用这些节点。</div></div>
      <div class="field"><label class="flex" style="gap:8px;align-items:center"><input type="checkbox" v-model="srcForm.enabled"> 启用（定时自动抓取）</label></div>
      <button class="btn primary block" @click="saveSrc">保存</button>
    </modal>
  </div>`,
});

/* ===== ADMIN HELP DOCS ===== */
A.component('view-admin-help', {
  data() { return { docs: [], ed: null }; },
  async mounted() { await this.load(); },
  computed: {
    charCount() { return (this.ed && this.ed.content || '').length; },
  },
  methods: {
    async load() { this.docs = await this.$api('/api/admin/help') || []; },
    create() { this.ed = { id: 0, title: '', content: '', sort_order: (this.docs.length + 1) * 10, published: true, mode: 'split' }; },
    edit(d) { this.ed = Object.assign({ mode: 'split' }, d); },
    async save() {
      const e = this.ed;
      if (!(e.title || '').trim()) { this.$toast('请填写标题', true); return; }
      const body = { title: e.title, content: e.content, sort_order: Number(e.sort_order) || 0, published: !!e.published };
      try {
        if (e.id) await this.$api('/api/admin/help/' + e.id, { method: 'PUT', body });
        else await this.$api('/api/admin/help', { method: 'POST', body });
        this.$toast('已保存'); this.ed = null; await this.load();
      } catch (err) { this.$toast(err.message, true); }
    },
    async del(d) { if (!confirm('删除帮助文档「' + d.title + '」？')) return; await this.$api('/api/admin/help/' + d.id, { method: 'DELETE' }); this.$toast('已删除'); await this.load(); },
    // --- markdown editor helpers (wrap selection / insert block at caret) ---
    ins(before, after, ph) {
      const ta = this.$refs.ta; if (!ta) return;
      after = after || ''; ph = ph || '';
      const v = this.ed.content || '', s = ta.selectionStart, e = ta.selectionEnd, sel = v.slice(s, e) || ph;
      this.ed.content = v.slice(0, s) + before + sel + after + v.slice(e);
      this.$nextTick(() => { ta.focus(); const p = s + before.length; ta.setSelectionRange(p, p + sel.length); });
    },
    block(text) {
      const ta = this.$refs.ta; if (!ta) return;
      const v = this.ed.content || '', s = ta.selectionStart, pre = s > 0 && v[s - 1] !== '\n' ? '\n' : '';
      this.ed.content = v.slice(0, s) + pre + text + v.slice(s);
      this.$nextTick(() => { ta.focus(); const p = s + pre.length + text.length; ta.setSelectionRange(p, p); });
    },
    onTab(ev) {
      const ta = ev.target, s = ta.selectionStart, e = ta.selectionEnd, v = this.ed.content || '';
      this.ed.content = v.slice(0, s) + '  ' + v.slice(e);
      this.$nextTick(() => { ta.focus(); ta.setSelectionRange(s + 2, s + 2); });
    },
    tH() { this.ins('## ', '', '标题'); },
    tB() { this.ins('**', '**', '加粗文字'); },
    tI() { this.ins('*', '*', '斜体文字'); },
    tLink() { this.ins('[', '](https://)', '链接文字'); },
    tCode() { const b = String.fromCharCode(96); this.ins(b, b, '代码'); },
    tPre() { const f = String.fromCharCode(96, 96, 96); this.block(f + '\n代码块\n' + f + '\n'); },
    tUl() { this.ins('- ', '', '列表项'); },
    tOl() { this.ins('1. ', '', '列表项'); },
    tQuote() { this.ins('> ', '', '引用内容'); },
    tHr() { this.block('---\n'); },
    tTable() { this.block('| 列1 | 列2 | 列3 |\n| --- | --- | --- |\n| 内容 | 内容 | 内容 |\n'); },
  },
  template: `
  <div>
    <h1 class="page-title">帮助文档</h1><div class="page-sub">编辑面向用户的帮助文档，支持 Markdown（标题 / 加粗 / 列表 / 表格 / 代码块 / 链接）</div>
    <div class="card">
      <div class="card-h"><h3>文档列表</h3><button class="btn sm primary" @click="create">新建文档</button></div>
      <div v-if="!docs.length" class="empty small">还没有帮助文档，点「新建文档」开始</div>
      <table v-else class="table"><thead><tr><th style="width:64px">排序</th><th>标题</th><th style="width:88px">状态</th><th style="width:130px"></th></tr></thead>
      <tbody><tr v-for="d in docs" :key="d.id">
        <td class="dim">{{d.sort_order}}</td>
        <td>{{d.title}}</td>
        <td><span class="badge" :class="d.published?'green':''">{{d.published?'已发布':'草稿'}}</span></td>
        <td><div class="flex" style="gap:6px"><button class="btn sm" @click="edit(d)">编辑</button><button class="btn sm danger" @click="del(d)">删除</button></div></td>
      </tr></tbody></table>
    </div>

    <modal v-if="ed" :title="ed.id?'编辑文档':'新建文档'" :wide="true" @close="ed=null">
      <div class="row">
        <div class="field" style="flex:3"><label>标题</label><input class="input" v-model="ed.title" placeholder="如：使用说明"></div>
        <div class="field" style="flex:1"><label>排序</label><input class="input" type="number" v-model="ed.sort_order"></div>
      </div>
      <label class="flex" style="margin:2px 0 14px"><input type="checkbox" v-model="ed.published"> <span>已发布（用户可见）</span></label>

      <div class="mdedit-bar">
        <button type="button" class="tb" title="标题" @click="tH">H</button>
        <button type="button" class="tb" title="加粗" @click="tB"><b>B</b></button>
        <button type="button" class="tb" title="斜体" @click="tI"><i>I</i></button>
        <span class="sep"></span>
        <button type="button" class="tb" title="链接" @click="tLink">🔗</button>
        <button type="button" class="tb" title="行内代码" @click="tCode">&lt;/&gt;</button>
        <button type="button" class="tb" title="代码块" @click="tPre">▤</button>
        <span class="sep"></span>
        <button type="button" class="tb" title="无序列表" @click="tUl">•—</button>
        <button type="button" class="tb" title="有序列表" @click="tOl">1.</button>
        <button type="button" class="tb" title="引用" @click="tQuote">❝</button>
        <button type="button" class="tb" title="表格" @click="tTable">▦</button>
        <button type="button" class="tb" title="分隔线" @click="tHr">―</button>
        <span class="grow"></span>
        <div class="mdedit-seg">
          <button type="button" :class="{on:ed.mode==='write'}" @click="ed.mode='write'">编辑</button>
          <button type="button" :class="{on:ed.mode==='split'}" @click="ed.mode='split'">分屏</button>
          <button type="button" :class="{on:ed.mode==='preview'}" @click="ed.mode='preview'">预览</button>
        </div>
      </div>
      <div class="mdedit-body">
        <div class="pane" v-show="ed.mode!=='preview'">
          <textarea ref="ta" class="mdedit-ta mono" v-model="ed.content" @keydown.tab.prevent="onTab" placeholder="在此输入 Markdown…&#10;&#10;| 客户端 | 平台 | 下载 |&#10;| --- | --- | --- |&#10;| Clash Verge | PC | 链接 |"></textarea>
        </div>
        <div class="pane" v-show="ed.mode!=='write'">
          <div class="md mdedit-pv" v-html="$md(ed.content) || '<span style=&quot;color:var(--text-3)&quot;>预览区（实时渲染）…</span>'"></div>
        </div>
      </div>
      <div class="mdedit-foot">
        <span class="mdedit-count">{{charCount}} 字</span>
        <span class="grow" style="flex:1"></span>
        <button class="btn" @click="ed=null">取消</button>
        <button class="btn primary" @click="save">保存</button>
      </div>
    </modal>
  </div>`,
});

/* ===== ADMIN SETTINGS ===== */
A.component('view-admin-settings', {
  data() { return { s: {}, groups: [], smtpTo: '', saving: false, trafficGB: 0, resyncing: false }; },
  async mounted() { this.s = await this.$api('/api/admin/settings') || {}; this.groups = await this.$api('/api/admin/node-groups') || []; this.trafficGB = (Number(this.s.default_traffic) || 0) / 1073741824; if (!this.s.register_mode) this.s.register_mode = this.bool('registration_open') ? 'open' : 'closed'; },
  computed: { envKeys() { return (this.s._env_keys || '').split(',').filter(Boolean); } },
  methods: {
    isEnv(k) { return this.envKeys.includes(k); },
    bool(k) { return this.s[k] === 'true' || this.s[k] === '1'; },
    setBool(k, v) { this.s[k] = v ? 'true' : 'false'; },
    async save() { this.saving = true; this.s.default_traffic = String(Math.round(Number(this.trafficGB) * 1073741824)); try { this.s = await this.$api('/api/admin/settings', { method: 'PUT', body: this.s }); this.trafficGB = (Number(this.s.default_traffic) || 0) / 1073741824; this.$toast('已保存（部分设置如 SMTP 需重启服务生效）'); } catch (e) { this.$toast(e.message, true); } this.saving = false; },
    async testSmtp() { try { await this.$api('/api/admin/settings/test-smtp', { method: 'POST', body: { to: this.smtpTo } }); this.$toast('测试邮件已发送'); } catch (e) { this.$toast(e.message, true); } },
    async resyncNodes() {
      if (!confirm('立即重建并下发 sing-box 配置？\n无套餐且无免费分组的用户将被停用，其节点立即失效。')) return;
      this.resyncing = true;
      try { const r = await this.$api('/api/admin/rebuild', { method: 'POST' }); this.$toast('已重建，覆盖 ' + r.synced + ' 人，其中停用 ' + r.disabled_no_access + ' 人'); }
      catch (e) { this.$toast(e.message, true); }
      this.resyncing = false;
    },
  },
  template: `
  <div>
    <div class="between mb"><div><h1 class="page-title">系统设置</h1><div class="muted small">注册、邮件、默认额度与订阅模板</div></div><button class="btn primary" :disabled="saving" @click="save"><span v-if="saving" class="spin"></span><span v-else>保存设置</span></button></div>

    <div class="card mb"><div class="card-h"><h3>注册 & 验证</h3></div>
      <div class="field"><label>注册模式</label><select v-model="s.register_mode"><option value="open">开放注册（任何人可注册）</option><option value="code">需要注册码</option><option value="closed">关闭注册</option></select></div>
      <label class="between" style="padding:8px 0;border-top:1px solid var(--border-soft)"><span>注册需邮箱验证</span><input type="checkbox" :checked="bool('email_verify_required')" @change="setBool('email_verify_required',$event.target.checked)"></label>
    </div>

    <div class="card mb"><div class="card-h"><h3>默认额度（新用户）</h3></div>
      <div class="row">
        <div class="field"><label>默认流量（GB，0=不限）</label><input class="input" type="number" step="0.1" v-model.number="trafficGB"></div>
        <div class="field"><label>默认有效期（天）</label><input class="input" v-model="s.default_expiry_days"></div>
      </div>
      <div class="row">
        <div class="field"><label>积分汇率（X积分=1元）</label><input class="input" v-model="s.points_per_cny"></div>
        <div class="field"><label>注册赠送积分</label><input class="input" v-model="s.signup_bonus_points"></div>
      </div>
      <div class="field"><label>免费/默认节点分组（无套餐用户也可用）</label><select v-model="s.free_group_id"><option value="">无</option><option v-for="g in groups" :value="String(g.id)">{{g.name}}</option></select></div>
    </div>

    <div class="card mb"><div class="card-h"><h3>节点下发</h3></div>
      <div class="small dim" style="padding:8px 0">客户端连接地址从<a href="#/admin/servers">服务器管理</a>中自动获取，无需手动填写。重建节点配置请到<a href="#/admin/servers">服务器管理</a>页面操作。</div>
    </div>

    <div class="card mb"><div class="card-h"><h3>SMTP 邮件</h3><span class="small dim" v-if="isEnv('smtp_host')">当前由环境变量(QZ_SMTP_*)配置、优先生效</span></div>
      <div class="row">
        <div class="field"><label>服务器</label><input class="input" v-model="s.smtp_host" placeholder="smtp.example.com"></div>
        <div class="field"><label>端口</label><input class="input" v-model="s.smtp_port" placeholder="465 / 587"></div>
        <div class="field"><label>加密</label><select v-model="s.smtp_security"><option value="">自动</option><option value="ssl">SSL(465)</option><option value="starttls">STARTTLS(587)</option><option value="none">无</option></select></div>
      </div>
      <div class="row">
        <div class="field"><label>账号</label><input class="input" v-model="s.smtp_user"></div>
        <div class="field"><label>密码</label><input class="input" type="password" v-model="s.smtp_pass" placeholder="留 *** 表示不修改"></div>
      </div>
      <div class="row">
        <div class="field"><label>发件人地址</label><input class="input" v-model="s.smtp_from"></div>
        <div class="field"><label>发件人名称</label><input class="input" v-model="s.smtp_from_name" placeholder="轻舟"></div>
      </div>
      <div class="flex mt"><input class="input" style="max-width:240px" v-model="smtpTo" placeholder="测试收件邮箱"><button class="btn sm" @click="testSmtp">发送测试邮件</button></div>
    </div>

    <div class="card"><div class="card-h"><h3>订阅防泄漏模板（留空用内置）</h3></div>
      <div class="field"><label>Clash 模板 (YAML)</label><textarea style="min-height:120px" class="mono" v-model="s.sub_clash_template" placeholder="dns / tun / sniffer ..."></textarea></div>
      <div class="field"><label>sing-box 模板 (JSON)</label><textarea style="min-height:120px" class="mono" v-model="s.sub_singbox_template"></textarea></div>
    </div>
  </div>`,
});

/* ===== ADMIN REG CODES ===== */
A.component('view-admin-regcodes', {
  data() { return { list: [], gen: { count: 10, max_uses: 1, note: '' }, generated: [], busy: false }; },
  async mounted() { await this.load(); },
  methods: {
    async load() { this.list = await this.$api('/api/admin/reg-codes') || []; },
    async generate() {
      this.busy = true;
      try { const r = await this.$api('/api/admin/reg-codes/generate', { method: 'POST', body: { count: Number(this.gen.count), max_uses: Number(this.gen.max_uses), note: this.gen.note } }); this.generated = r.codes || []; this.$toast('已生成 ' + this.generated.length + ' 个'); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
    async toggle(c) { await this.$api('/api/admin/reg-codes/' + c.id, { method: 'PUT', body: { enabled: !c.enabled } }); this.load(); },
    async del(c) { if (!confirm('删除注册码 ' + c.code + '？')) return; await this.$api('/api/admin/reg-codes/' + c.id, { method: 'DELETE' }); this.load(); },
    status(c) { if (!c.enabled) return { t: '已停用', cls: '' }; if (c.max_uses > 0 && c.used >= c.max_uses) return { t: '已用完', cls: 'red' }; return { t: '可用', cls: 'green' }; },
    usedUp(c) { return c.max_uses > 0 && c.used >= c.max_uses; },
    copyAll() { this.$copy(this.generated.join('\n'), '已复制 ' + this.generated.length + ' 个注册码'); },
  },
  template: `
  <div>
    <h1 class="page-title">注册码</h1><div class="page-sub">「注册模式=需要注册码」时，用户注册需填写有效注册码</div>
    <div class="card mb">
      <div class="card-h"><h3>批量生成</h3></div>
      <div class="row" style="align-items:flex-end">
        <div class="field"><label>数量</label><input class="input" type="number" v-model="gen.count"></div>
        <div class="field"><label>每个可用次数（0=不限）</label><input class="input" type="number" v-model="gen.max_uses"></div>
        <div class="field"><label>备注（可选）</label><input class="input" v-model="gen.note"></div>
        <div class="field" style="flex:none"><button class="btn primary" :disabled="busy" @click="generate"><span v-if="busy" class="spin"></span><span v-else>生成</span></button></div>
      </div>
      <div v-if="generated.length" class="mt">
        <div class="between mb"><span class="small muted">本次生成（请复制保存）</span><button class="btn sm" @click="copyAll">复制全部</button></div>
        <textarea class="input mono" style="min-height:90px" readonly :value="generated.join('\\n')"></textarea>
      </div>
    </div>
    <div class="card">
      <div class="card-h"><h3>注册码列表</h3><span class="small dim">{{list.length}} 个</span></div>
      <div v-if="!list.length" class="empty small">还没有注册码</div>
      <table v-else class="table"><thead><tr><th>注册码</th><th>使用</th><th>状态</th><th>备注</th><th>创建</th><th></th></tr></thead>
      <tbody><tr v-for="c in list" :key="c.id">
        <td><b class="mono">{{c.code}}</b> <a class="small" style="cursor:pointer" @click="$copy(c.code,'已复制')">复制</a></td>
        <td>{{c.used}} / {{c.max_uses>0?c.max_uses:'∞'}}</td>
        <td><span class="badge" :class="status(c).cls">{{status(c).t}}</span></td>
        <td class="small dim">
          <div v-if="c.note">{{c.note}}</div>
          <div v-for="u in (c.uses||[])" :key="u.user_id+'-'+u.used_at" style="line-height:1.5">
            <span>{{$dt(u.used_at)}}</span> · <b>{{u.username||('#'+u.user_id)}}</b><span v-if="u.email"> · {{u.email}}</span>
          </div>
          <span v-if="!c.note && !(c.uses||[]).length">—</span>
        </td>
        <td class="small dim">{{$date(c.created_at)}}</td>
        <td><div class="flex"><button v-if="!usedUp(c)" class="btn sm" @click="toggle(c)">{{c.enabled?'停用':'启用'}}</button><button class="btn sm danger" @click="del(c)">删</button></div></td>
      </tr></tbody></table>
    </div>
  </div>`,
});

/* ===== ADMIN ANNOUNCEMENTS ===== */
A.component('view-admin-announcements', {
  data() { return { list: [], editing: null }; },
  async mounted() { await this.load(); },
  methods: {
    async load() { this.list = await this.$api('/api/admin/announcements') || []; },
    toLocal(u) { if (!u) return ''; const d = new Date(u * 1000), p = n => String(n).padStart(2, '0'); return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + 'T' + p(d.getHours()) + ':' + p(d.getMinutes()); },
    fromLocal(s) { return s ? Math.floor(new Date(s).getTime() / 1000) : 0; },
    edit(a) { this.editing = a ? { ...a, startStr: this.toLocal(a.start_at), endStr: this.toLocal(a.end_at) } : { title: '', content: '', pinned: false, enabled: true, startStr: '', endStr: '' }; },
    async save() {
      const e = this.editing;
      if (!e.title) { this.$toast('标题不能为空', true); return; }
      e.start_at = this.fromLocal(e.startStr); e.end_at = this.fromLocal(e.endStr);
      try { if (e.id) await this.$api('/api/admin/announcements/' + e.id, { method: 'PUT', body: e }); else await this.$api('/api/admin/announcements', { method: 'POST', body: e }); this.editing = null; this.$toast('已保存'); this.load(); }
      catch (err) { this.$toast(err.message, true); }
    },
    async del(a) { if (!confirm('删除公告「' + a.title + '」？')) return; await this.$api('/api/admin/announcements/' + a.id, { method: 'DELETE' }); this.load(); },
  },
  template: `
  <div>
    <div class="between mb"><div><h1 class="page-title">通知公告</h1><div class="muted small">发布给所有用户，显示在他们的仪表盘</div></div><button class="btn primary" @click="edit(null)">+ 新建公告</button></div>
    <div class="card">
      <div v-if="!list.length" class="empty">还没有公告</div>
      <table v-else class="table"><thead><tr><th>标题</th><th>状态</th><th>更新时间</th><th></th></tr></thead>
      <tbody><tr v-for="a in list" :key="a.id">
        <td><b>{{a.title}}</b> <span v-if="a.pinned" class="badge amber">置顶</span></td>
        <td><span class="badge" :class="a.enabled?'green':''">{{a.enabled?'显示':'隐藏'}}</span></td>
        <td class="small dim">{{$date(a.updated_at)}}</td>
        <td><div class="flex"><button class="btn sm" @click="edit(a)">编辑</button><button class="btn sm danger" @click="del(a)">删</button></div></td>
      </tr></tbody></table>
    </div>
    <modal v-if="editing" :title="editing.id?'编辑公告':'新建公告'" @close="editing=null">
      <div class="field"><label>标题</label><input class="input" v-model="editing.title"></div>
      <div class="field"><label>内容</label><textarea style="min-height:140px" v-model="editing.content" placeholder="支持换行"></textarea></div>
      <div class="row">
        <div class="field"><label>生效时间（可空=立即）</label><input class="input" type="datetime-local" v-model="editing.startStr"></div>
        <div class="field"><label>结束时间（可空=长期）</label><input class="input" type="datetime-local" v-model="editing.endStr"></div>
      </div>
      <div class="flex wrap" style="gap:18px;margin:6px 0 16px">
        <label class="flex"><input type="checkbox" v-model="editing.pinned"> <span>置顶</span></label>
        <label class="flex"><input type="checkbox" v-model="editing.enabled"> <span>显示给用户</span></label>
      </div>
      <button class="btn primary block" @click="save">保存</button>
    </modal>
  </div>`,
});

/* ===== ADMIN NATIVE SING-BOX (B2) ===== */
A.component('view-admin-singbox', {
  data() { return { tls: [], inbounds: [], servers: [], te: null, ib: null, preview: '', previewServer: 0, busy: false }; },
  async mounted() { await this.load(); },
  methods: {
    async load() {
      this.tls = await this.$api('/api/admin/sb/tls') || [];
      this.inbounds = await this.$api('/api/admin/sb/inbounds') || [];
      this.servers = await this.$api('/api/admin/servers') || [];
    },
    tlsName(id) { const t = this.tls.find(x => x.id === id); return t ? t.name : (id ? ('#' + id) : '无'); },
    serverName(sid) { if (!sid) return '本机'; const s = this.servers.find(x => x.id === sid); return s ? s.name : ('#' + sid); },
    tlsUse(id) { return this.inbounds.filter(n => n.tls_id === id).length; },
    async toggleInbound(n) {
      try { await this.$api('/api/admin/sb/inbounds/' + n.id, { method: 'PUT', body: { ...n, enabled: !n.enabled } }); n.enabled = !n.enabled; this.$toast(n.enabled ? '已启用' : '已停用'); }
      catch (e) { this.$toast(e.message, true); }
    },
    PT() { return { vless: 'VLESS', hysteria2: 'Hysteria2', tuic: 'TUIC', trojan: 'Trojan', vmess: 'VMess', shadowsocks: 'Shadowsocks', anytls: 'AnyTLS', hysteria: 'Hysteria v1' }; },
    jp(s) { try { return JSON.parse(s || '{}'); } catch (e) { return {}; } },
    // ---- TLS (fully visual) ----
    newTls() { this.te = { id: 0, server_id: this.servers.length ? this.servers[0].id : 0, mode: 'reality', name: '', server_name: 'www.microsoft.com', handshake_server: '', handshake_port: 443, certificate: '', key: '', insecure: false, alpn: ['h3', 'h2', 'http/1.1'], public_key: '', private_key: '', short_id: '', fingerprint: 'chrome', min_version: '', max_version: '' }; },
    editTls(t) {
      const s = this.jp(t.server_json), c = this.jp(t.client_json);
      const r = s.reality || {}, hs = r.handshake || {};
      this.te = {
        id: t.id, server_id: t.server_id || 0, mode: t.mode, name: t.name, server_name: s.server_name || '',
        handshake_server: hs.server || '', handshake_port: hs.server_port || 443,
        certificate: s.certificate || '', key: s.key || '', insecure: !!c.insecure, alpn: s.alpn || [],
        public_key: (c.reality && c.reality.public_key) || '',
        private_key: r.private_key || '',
        short_id: c.short_id || (Array.isArray(r.short_id) ? r.short_id[0] : r.short_id) || '',
        fingerprint: (c.utls && c.utls.fingerprint) || 'chrome', min_version: s.min_version || '', max_version: s.max_version || '',
      };
    },
    toggleAlpn(a) { const i = this.te.alpn.indexOf(a); if (i < 0) this.te.alpn.push(a); else this.te.alpn.splice(i, 1); },
    async genRealityKeys() {
      try {
        const r = await this.$api('/api/admin/sb/reality-keypair', { method: 'POST' });
        this.te.private_key = r.private_key;
        this.te.public_key = r.public_key;
        this.te.short_id = r.short_id;
        this.$toast('密钥已生成 ✨');
      } catch (e) { this.$toast(e.message, true); }
    },
    async saveTls() {
      const e = this.te;
      if (!e.name.trim() || !e.server_name.trim()) { this.$toast('名称和 SNI 必填', true); return; }
      this.busy = true;
      try {
        if (e.mode === 'reality') {
          const body = { name: e.name, server_id: Number(e.server_id) || 0, server_name: e.server_name, handshake_server: e.handshake_server, handshake_port: Number(e.handshake_port) || 443, fingerprint: e.fingerprint, private_key: e.private_key || '', public_key: e.public_key || '', short_id: e.short_id || '' };
          if (e.id) await this.$api('/api/admin/sb/tls/reality/' + e.id, { method: 'PUT', body });
          else await this.$api('/api/admin/sb/tls/reality', { method: 'POST', body });
        } else {
          if (!e.id && (!e.certificate.trim() || !e.key.trim())) { this.$toast('证书和私钥必填', true); this.busy = false; return; }
          const body = { name: e.name, server_id: Number(e.server_id) || 0, server_name: e.server_name, certificate: e.certificate, key: e.key, insecure: e.insecure, alpn: e.alpn, fingerprint: e.fingerprint, min_version: e.min_version, max_version: e.max_version };
          if (e.id) await this.$api('/api/admin/sb/tls/cert/' + e.id, { method: 'PUT', body });
          else await this.$api('/api/admin/sb/tls/cert', { method: 'POST', body });
        }
        this.$toast('已保存'); this.te = null; await this.load();
      } catch (err) { this.$toast(err.message, true); }
      this.busy = false;
    },
    async delTls(t) { if (!confirm('删除 TLS「' + t.name + '」？被入站引用时会失败。')) return; try { await this.$api('/api/admin/sb/tls/' + t.id, { method: 'DELETE' }); this.load(); } catch (e) { this.$toast(e.message, true); } },
    // ---- inbounds (fully visual) ----
    hasTransport(t) { return t === 'vless' || t === 'vmess' || t === 'trojan'; },
    hasMux(t) { return t === 'vless' || t === 'vmess' || t === 'trojan' || t === 'shadowsocks' || t === 'anytls'; },
    newInbound() { this.ib = { id: 0, type: 'vless', tag: '', listen_port: 443, tls_id: 0, server_id: this.servers.length ? this.servers[0].id : 0, enabled: true, tfo: false, cc: 'bbr', zero_rtt: false, up_mbps: 0, down_mbps: 0, obfs_password: '', masquerade: '', net: 'tcp', ws_path: '/', grpc_service: '', ss_method: '2022-blake3-aes-128-gcm', mux: false, brutal: false, brutal_up: 0, brutal_down: 0, mptcp: false }; },
    editInbound(n) {
      const o = this.jp(n.options);
      const obfs = o.obfs || {}, masq = o.masquerade, tr = o.transport || {}, mx = o.multiplex || {}, br = mx.brutal || {};
      this.ib = {
        id: n.id, type: n.type, tag: n.tag, listen_port: n.listen_port, tls_id: n.tls_id, server_id: n.server_id || 0, enabled: n.enabled,
        tfo: !!o.tcp_fast_open, cc: o.congestion_control || 'bbr', zero_rtt: !!o.zero_rtt_handshake,
        up_mbps: o.up_mbps || 0, down_mbps: o.down_mbps || 0,
        obfs_password: obfs.password || '', masquerade: typeof masq === 'string' ? masq : (masq && masq.url) || '',
        net: tr.type || 'tcp', ws_path: tr.path || '/', grpc_service: tr.service_name || '',
        ss_method: o.method || '2022-blake3-aes-128-gcm',
        mux: !!mx.enabled, brutal: !!br.enabled, brutal_up: br.up_mbps || 0, brutal_down: br.down_mbps || 0, mptcp: !!o.tcp_multi_path,
      };
    },
    async saveInbound() {
      const b = this.ib;
      if (!b.tag.trim()) { this.$toast('请填写名称/Tag', true); return; }
      const o = {};
      if (b.tfo) o.tcp_fast_open = true;
      if (b.type === 'tuic') { o.congestion_control = b.cc; if (b.zero_rtt) o.zero_rtt_handshake = true; }
      if (b.type === 'hysteria2') {
        if (Number(b.up_mbps) > 0) o.up_mbps = Number(b.up_mbps);
        if (Number(b.down_mbps) > 0) o.down_mbps = Number(b.down_mbps);
        if (b.obfs_password.trim()) o.obfs = { type: 'salamander', password: b.obfs_password.trim() };
        if (b.masquerade.trim()) o.masquerade = { type: 'proxy', url: b.masquerade.trim() };
      }
      if (this.hasTransport(b.type) && b.net && b.net !== 'tcp') {
        if (b.net === 'ws') o.transport = { type: 'ws', path: b.ws_path.trim() || '/' };
        else if (b.net === 'httpupgrade') o.transport = { type: 'httpupgrade', path: b.ws_path.trim() || '/' };
        else if (b.net === 'grpc') o.transport = { type: 'grpc', service_name: b.grpc_service.trim() };
      }
      if (b.type === 'hysteria') {
        if (Number(b.up_mbps) > 0) o.up_mbps = Number(b.up_mbps);
        if (Number(b.down_mbps) > 0) o.down_mbps = Number(b.down_mbps);
      }
      if (b.type === 'shadowsocks') o.method = b.ss_method; // server PSK auto-generated server-side
      if (this.hasMux(b.type) && b.mux) {
        o.multiplex = { enabled: true, padding: false };
        if (b.brutal && Number(b.brutal_up) > 0 && Number(b.brutal_down) > 0) {
          o.multiplex.brutal = { enabled: true, up_mbps: Number(b.brutal_up), down_mbps: Number(b.brutal_down) };
        }
      }
      if (b.mptcp && this.hasTransport(b.type)) o.tcp_multi_path = true;
      const tlsId = b.type === 'shadowsocks' ? 0 : (Number(b.tls_id) || 0); // SS has its own encryption
      const body = { type: b.type, tag: b.tag.trim(), listen: '::', listen_port: Number(b.listen_port) || 0, tls_id: tlsId, server_id: Number(b.server_id) || 0, options: JSON.stringify(o), enabled: b.enabled };
      const u = '/api/admin/sb/inbounds' + (b.id ? '/' + b.id : '');
      this.busy = true;
      try { await this.$api(u, { method: b.id ? 'PUT' : 'POST', body }); this.$toast('已保存入站'); this.ib = null; await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
    async delInbound(n) { if (!confirm('删除入站「' + n.tag + '」？')) return; await this.$api('/api/admin/sb/inbounds/' + n.id, { method: 'DELETE' }); this.load(); },
    async showPreview(sid) {
      this.previewServer = sid || 0;
      try { const r = await fetch('/api/admin/sb/preview?server_id=' + this.previewServer, { headers: { Authorization: 'Bearer ' + this.store.token }, credentials: 'include' }); this.preview = await r.text(); }
      catch (e) { this.$toast('预览失败', true); }
    },
  },
  template: `
  <div>
    <h1 class="page-title">节点配置</h1><div class="page-sub">配置 TLS 加密与入站协议</div>
    <div class="card mb" style="background:var(--accent-soft);border:none"><div class="small">① 先建 <b>TLS</b>（VLESS 选 Reality，TUIC / HY2 用证书）→ ② 再建 <b>入站</b> 并选用它。</div></div>

    <div class="card mb">
      <div class="card-h"><h3>TLS 加密</h3><button class="btn primary sm" @click="newTls">+ 新建 TLS</button></div>
      <div v-if="!tls.length" class="empty">还没有 TLS。VLESS 必须用 Reality —— 点「新建 TLS」选 Reality 即可自动生成密钥。</div>
      <table v-else class="table"><thead><tr><th>名称</th><th>类型</th><th>SNI 伪装域名</th><th>服务器</th><th>入站数</th><th class="ta-r">操作</th></tr></thead>
      <tbody><tr v-for="t in tls" :key="t.id">
        <td><b class="mono">{{t.name}}</b></td>
        <td><span class="badge" :class="t.mode==='reality'?'green':'blue'">{{t.mode==='reality'?'Reality':'证书 TLS'}}</span></td>
        <td class="small dim">{{jp(t.server_json).server_name||'—'}}</td>
        <td class="small dim">{{serverName(t.server_id)}}</td>
        <td class="small dim">{{tlsUse(t.id)||'—'}}</td>
        <td class="ta-r"><button class="btn sm ghost" @click="editTls(t)">编辑</button> <button class="btn sm ghost danger" @click="delTls(t)">删除</button></td>
      </tr></tbody></table>
    </div>

    <div class="card mb">
      <div class="card-h"><h3>入站协议</h3><div class="flex"><select class="input" style="width:auto;display:inline-block" v-model="previewServer"><option :value="0">本机</option><option v-for="s in servers" :key="s.id" :value="s.id">{{s.name}}</option></select> <button class="btn sm" @click="showPreview(previewServer)">预览配置</button><button class="btn primary sm" @click="newInbound">+ 新建入站</button></div></div>
      <div v-if="!inbounds.length" class="empty">还没有入站。新建后绑定到「节点分组」里的自建节点即可下发给用户。</div>
      <table v-else class="table"><thead><tr><th>名称 / TAG</th><th>协议</th><th>端口</th><th>服务器</th><th>TLS</th><th>状态</th><th class="ta-r">操作</th></tr></thead>
      <tbody><tr v-for="n in inbounds" :key="n.id">
        <td><b class="mono">{{n.tag}}</b></td>
        <td><span class="badge blue xs">{{PT()[n.type]||n.type}}</span></td>
        <td class="small mono dim">{{n.listen_port}}</td>
        <td class="small dim">{{serverName(n.server_id)}}</td>
        <td class="small dim">{{tlsName(n.tls_id)}}</td>
        <td><span class="badge" :class="n.enabled?'green':'red'" style="cursor:pointer" :title="n.enabled?'点击停用':'点击启用'" @click="toggleInbound(n)">{{n.enabled?'● 启用':'○ 停用'}}</span></td>
        <td class="ta-r"><button class="btn sm ghost" @click="editInbound(n)">编辑</button> <button class="btn sm ghost danger" @click="delInbound(n)">删除</button></td>
      </tr></tbody></table>
    </div>

    <div v-if="preview" class="card">
      <div class="card-h"><h3>配置预览 · {{serverName(previewServer)}}</h3><button class="btn sm" @click="preview=''">关闭</button></div>
      <pre style="max-height:360px;overflow:auto;background:var(--bg-soft);padding:12px;border-radius:8px;font-size:12px;white-space:pre-wrap;word-break:break-all">{{preview}}</pre>
    </div>

    <modal v-if="te" :title="te.id?'编辑 TLS':'新建 TLS'" @close="te=null">
      <div class="field"><label>类型</label>
        <select v-model="te.mode" :disabled="!!te.id">
          <option value="reality">Reality（VLESS 专用，无需证书）</option>
          <option value="tls">证书 TLS（TUIC / Hysteria2）</option>
        </select>
        <div v-if="te.id" class="small dim">类型创建后不可更改</div>
      </div>
      <div class="field"><label>名称</label><input class="input" v-model="te.name" placeholder="如 reality-443"></div>
      <div class="field"><label>所属服务器</label><select v-model="te.server_id"><option :value="0">本机（面板所在服务器）</option><option v-for="s in servers" :key="s.id" :value="s.id">{{s.name}}（{{s.host}}）</option></select><div v-if="!servers.length" class="small dim">没有远程服务器，TLS 将用于本机入站</div></div>
      <div class="field"><label>SNI 伪装域名</label><input class="input" v-model="te.server_name" placeholder="www.microsoft.com">
        <div class="small dim">客户端可见的伪装域名。Reality 选一个高知名度 HTTPS 站点；证书 TLS 填你证书对应的域名。</div>
      </div>
      <div class="field"><label>uTLS 指纹（客户端 TLS 伪装）</label><select v-model="te.fingerprint">
        <option value="chrome">chrome（推荐）</option><option value="firefox">firefox</option><option value="safari">safari</option>
        <option value="ios">ios</option><option value="android">android</option><option value="edge">edge</option>
        <option value="random">random</option><option value="randomized">randomized</option>
      </select><div class="small dim">模拟主流浏览器的 TLS 指纹，抗指纹识别；会写进客户端订阅链接的 fp 参数</div></div>

      <template v-if="te.mode==='reality'">
        <div class="row">
          <div class="field"><label>握手目标（留空=同 SNI）</label><input class="input" v-model="te.handshake_server" placeholder="可留空"></div>
          <div class="field"><label>握手端口</label><input class="input" type="number" v-model="te.handshake_port"></div>
        </div>

        <div class="field" style="margin:12px 0">
          <button class="btn primary" type="button" @click="genRealityKeys">🔑 一键生成 Reality 密钥对</button>
          <div class="small dim">点击生成全新的 x25519 私钥、公钥和 short_id，用于 VLESS Reality 加密</div>
        </div>

        <div v-if="te.private_key || te.public_key" style="background:var(--bg-soft);padding:12px;border-radius:8px;margin:8px 0">
          <div class="field"><label>私钥（服务端用，保存后加密存储）</label><input class="input" :value="te.private_key" readonly style="font-family:monospace;font-size:12px" @click="$el.select();document.execCommand('copy');$toast('已复制')"></div>
          <div class="field"><label>公钥（客户端 pbk= 参数）</label><input class="input" :value="te.public_key" readonly style="font-family:monospace;font-size:12px" @click="$el.select();document.execCommand('copy');$toast('已复制')"></div>
          <div class="field"><label>Short ID</label><input class="input" :value="te.short_id" readonly style="font-family:monospace;font-size:12px" @click="$el.select();document.execCommand('copy');$toast('已复制')"></div>
        </div>

        <div class="small dim mb">密钥保存后加密存储；编辑只改 SNI/握手，密钥保持不变。如需更换密钥，删除后重新创建即可。</div>
      </template>

      <template v-else>
        <div class="field"><label>证书 PEM</label><textarea style="min-height:90px;font-family:monospace;font-size:11px" v-model="te.certificate" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
        <div class="field"><label>私钥 PEM</label><textarea style="min-height:70px;font-family:monospace;font-size:11px" v-model="te.key" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></div>
        <div class="field"><label>ALPN</label>
          <div class="flex wrap"><span v-for="a in ['h3','h2','http/1.1']" class="chip" :class="{on:te.alpn.includes(a)}" @click="toggleAlpn(a)">{{a}}</span></div>
          <div class="small dim">TUIC 通常用 h3；HY2 可不选。</div>
        </div>
        <div class="row">
          <div class="field"><label>最低 TLS 版本</label><select v-model="te.min_version"><option value="">默认</option><option value="1.2">1.2</option><option value="1.3">1.3</option></select></div>
          <div class="field"><label>最高 TLS 版本</label><select v-model="te.max_version"><option value="">默认</option><option value="1.2">1.2</option><option value="1.3">1.3</option></select></div>
        </div>
        <label class="between" style="padding:8px 0"><span>允许不安全证书（自签名时开启）<div class="small dim">证书与 SNI 域名不匹配/自签名时需开启，客户端链接会带 insecure=1</div></span><input type="checkbox" v-model="te.insecure"></label>
      </template>

      <button class="btn primary block" :disabled="busy" @click="saveTls"><span v-if="busy" class="spin"></span><span v-else>保存</span></button>
    </modal>

    <modal v-if="ib" :title="ib.id?'编辑入站':'新建入站'" @close="ib=null">
      <div class="row">
        <div class="field"><label>协议</label><select v-model="ib.type" :disabled="!!ib.id"><option value="vless">VLESS（配 Reality）</option><option value="hysteria2">Hysteria2</option><option value="tuic">TUIC</option><option value="trojan">Trojan</option><option value="vmess">VMess</option><option value="shadowsocks">Shadowsocks</option><option value="anytls">AnyTLS</option><option value="hysteria">Hysteria v1</option></select></div>
        <div class="field"><label>监听端口</label><input class="input" type="number" v-model="ib.listen_port"></div>
      </div>
      <div class="field"><label>名称 / Tag（唯一）</label><input class="input" v-model="ib.tag" placeholder="如 vless-香港"><div class="small dim">展示给用户的节点名，也是内部唯一标识</div></div>
      <div class="field"><label>所属服务器</label><select v-model="ib.server_id"><option :value="0">本机（面板所在服务器）</option><option v-for="s in servers" :key="s.id" :value="s.id">{{s.name}}（{{s.host}}）</option></select><div v-if="!servers.length" class="small dim">没有远程服务器，入站将部署到本机</div></div>
      <div v-if="ib.type==='shadowsocks'" class="field"><label>加密方式</label><select v-model="ib.ss_method">
        <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm（推荐）</option>
        <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
        <option value="2022-blake3-chacha20-poly1305">2022-blake3-chacha20-poly1305</option>
      </select><div class="small dim">Shadowsocks 自带加密，无需 TLS；服务端密钥自动生成，每用户密钥按其凭据派生</div></div>
      <div v-else class="field"><label>TLS / Reality</label>
        <select v-model="ib.tls_id"><option :value="0">无（不加密，不推荐）</option><option v-for="t in tls" :key="t.id" :value="t.id">{{t.name}}（{{t.mode==='reality'?'Reality':'证书'}}）</option></select>
        <div v-if="ib.type==='vless' && !ib.tls_id" class="small" style="color:var(--warn)">VLESS 建议选一个 Reality 配置</div>
      </div>

      <template v-if="ib.type==='tuic'">
        <div class="field"><label>拥塞控制</label><select v-model="ib.cc"><option value="bbr">bbr（推荐）</option><option value="cubic">cubic</option><option value="new_reno">new_reno</option></select></div>
        <label class="between" style="padding:8px 0"><span>0-RTT 握手<div class="small dim">更快连接，安全性略降</div></span><input type="checkbox" v-model="ib.zero_rtt"></label>
      </template>
      <div v-if="ib.type==='hysteria2'||ib.type==='hysteria'" class="row">
        <div class="field"><label>上行限速 Mbps（{{ib.type==='hysteria'?'建议填写':'0=不限'}}）</label><input class="input" type="number" v-model="ib.up_mbps"></div>
        <div class="field"><label>下行限速 Mbps（{{ib.type==='hysteria'?'建议填写':'0=不限'}}）</label><input class="input" type="number" v-model="ib.down_mbps"></div>
      </div>
      <template v-if="ib.type==='hysteria2'">
        <div class="field"><label>obfs 混淆密码（可选）</label><input class="input" v-model="ib.obfs_password" placeholder="留空=不混淆"><div class="small dim">salamander 混淆，抗封锁；客户端需配同样密码</div></div>
        <div class="field"><label>伪装回源 URL（可选）</label><input class="input" v-model="ib.masquerade" placeholder="如 https://news.ycombinator.com"><div class="small dim">被探测时伪装成该网站</div></div>
      </template>

      <template v-if="hasTransport(ib.type)">
        <div class="field"><label>传输层</label><select v-model="ib.net">
          <option value="tcp">TCP（默认，配 Reality 用这个）</option>
          <option value="ws">WebSocket（适合套 CDN）</option>
          <option value="grpc">gRPC</option>
          <option value="httpupgrade">HTTPUpgrade</option>
        </select><div v-if="ib.net!=='tcp' && tlsName(ib.tls_id).includes('Reality')" class="small" style="color:var(--warn)">Reality 仅支持 TCP，套 CDN 请用证书 TLS</div></div>
        <div v-if="ib.net==='ws'||ib.net==='httpupgrade'" class="field"><label>路径 Path</label><input class="input" v-model="ib.ws_path" placeholder="/"><div class="small dim">与客户端一致；套 CDN 时建议用不易猜的路径</div></div>
        <div v-if="ib.net==='grpc'" class="field"><label>gRPC ServiceName</label><input class="input" v-model="ib.grpc_service" placeholder="如 GunService"></div>
      </template>
      <template v-if="hasMux(ib.type)">
        <label class="between" style="padding:8px 0"><span>多路复用 Mux<div class="small dim">多连接复用一条，省握手；客户端需也开启</div></span><input type="checkbox" v-model="ib.mux"></label>
        <label v-if="ib.mux" class="between" style="padding:8px 0"><span>Brutal 拥塞加速<div class="small dim">需服务器装 TCP Brutal 内核模块，填上下行带宽</div></span><input type="checkbox" v-model="ib.brutal"></label>
        <div v-if="ib.mux && ib.brutal" class="row">
          <div class="field"><label>Brutal 上行 Mbps</label><input class="input" type="number" v-model="ib.brutal_up"></div>
          <div class="field"><label>Brutal 下行 Mbps</label><input class="input" type="number" v-model="ib.brutal_down"></div>
        </div>
      </template>
      <label v-if="hasTransport(ib.type)" class="between" style="padding:8px 0"><span>MPTCP 多路径<div class="small dim">多网卡/多路径 TCP，需双端支持</div></span><input type="checkbox" v-model="ib.mptcp"></label>
      <label class="between" style="padding:8px 0"><span>TCP Fast Open<div class="small dim">略微加速建连</div></span><input type="checkbox" v-model="ib.tfo"></label>
      <label class="flex" style="margin:6px 0 16px"><input type="checkbox" v-model="ib.enabled"> <span>启用此入站</span></label>
      <button class="btn primary block" :disabled="busy" @click="saveInbound"><span v-if="busy" class="spin"></span><span v-else>保存</span></button>
    </modal>
  </div>`,
});

/* ===== ADMIN ORDERS / CONSUMPTION ===== */
A.component('view-admin-orders', {
  data() { return { orders: [], q: '', busy: 0 }; },
  async mounted() { await this.load(); },
  computed: {
    revenue() { return this.orders.filter(o => o.status !== 'refunded').reduce((s, o) => s + o.price_points, 0); },
    refunded() { return this.orders.filter(o => o.status === 'refunded').reduce((s, o) => s + o.price_points, 0); },
  },
  methods: {
    async load() { this.orders = await this.$api('/api/admin/orders?q=' + encodeURIComponent(this.q)) || []; },
    ostatus(o) { return o.status === 'refunded' ? { t: '已退款', cls: 'amber' } : { t: '成功', cls: 'green' }; },
    async refund(o) {
      if (!confirm('确定为「' + (o.username || ('用户#' + o.user_id)) + '」退款订单 #' + o.id + '？\n将退回 ' + o.price_points + ' 积分，并撤销本次开通的流量/到期，订单标记为「已退款」（记录保留）。')) return;
      this.busy = o.id;
      try { const r = await this.$api('/api/admin/orders/' + o.id + '/refund', { method: 'POST' }); this.$toast('已退款，该用户余额 ' + r.points); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = 0;
    },
    async delOrder(o) {
      if (!confirm('该订单的用户已删除，无法退款。\n确定彻底删除订单记录 #' + o.id + '？此操作不可恢复。')) return;
      this.busy = o.id;
      try { await this.$api('/api/admin/orders/' + o.id, { method: 'DELETE' }); this.$toast('已删除记录 #' + o.id); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = 0;
    },
  },
  template: `
  <div>
    <h1 class="page-title">消费记录</h1><div class="page-sub">全站订单，可退款（退回积分并撤销本次开通）</div>
    <div class="grid cols-3 mb">
      <div class="card stat"><div class="label">有效消费（积分）</div><div class="value">{{revenue}}</div></div>
      <div class="card stat"><div class="label">已退款（积分）</div><div class="value">{{refunded}}</div></div>
      <div class="card stat"><div class="label">订单数</div><div class="value">{{orders.length}}</div></div>
    </div>
    <div class="card">
      <div class="card-h"><h3>订单列表</h3><div class="flex"><input class="input" style="width:180px" v-model="q" @keyup.enter="load" placeholder="按用户名搜索"><button class="btn sm" @click="load">搜索</button></div></div>
      <div v-if="!orders.length" class="empty">暂无订单</div>
      <table v-else class="table"><thead><tr><th>#</th><th>用户</th><th>商品</th><th>类型</th><th>积分</th><th>状态</th><th>时间</th><th></th></tr></thead>
      <tbody><tr v-for="o in orders" :key="o.id">
        <td class="small dim">{{o.id}}</td>
        <td><b v-if="o.username">{{o.username}}</b><span v-else class="badge xs amber">用户已删除</span></td>
        <td>{{o.name||'—'}}</td>
        <td class="small" style="white-space:nowrap">{{o.type==='plan'?'套餐':(o.type==='traffic'?'流量包':o.type)}}</td>
        <td>{{o.price_points}}</td>
        <td style="white-space:nowrap"><span class="badge" :class="ostatus(o).cls">{{ostatus(o).t}}</span></td>
        <td class="small dim" style="white-space:nowrap">{{$dt(o.created_at)}}</td>
        <td>
          <button v-if="o.status!=='refunded' && o.username" class="btn sm danger" :disabled="busy===o.id" @click="refund(o)"><span v-if="busy===o.id" class="spin"></span><span v-else>退款</span></button>
          <button v-else-if="!o.username" class="btn sm" :disabled="busy===o.id" @click="delOrder(o)"><span v-if="busy===o.id" class="spin"></span><span v-else>删除记录</span></button>
        </td>
      </tr></tbody></table>
    </div>
  </div>`,
});

/* ===== ADMIN SERVERS ===== */
A.component('view-admin-servers', {
  data() { return { servers: [], form: null, testing: 0, busy: false, rebuilding: 0, ir: null, irLoading: -1, irFiles: [], irPath: '', irServerId: 0, irServerName: '' }; },
  async mounted() { await this.load(); },
  methods: {
    async load() { this.servers = await this.$api('/api/admin/servers') || []; },
    blank() { return { name: '', host: '', port: 22, ssh_user: 'root', ssh_key: '', ssh_key_pass: '', ssh_password: '', config_path: '/etc/sing-box/config.json', systemd_unit: 'sing-box', sing_box_bin: '/usr/local/bin/sing-box', v2ray_listen: '', enabled: true }; },
    edit(s) { this.form = s ? { ...s } : this.blank(); },
    cancel() { this.form = null; },
    async save() {
      const f = this.form;
      if (!f.name.trim()) { this.$toast('名称不能为空', true); return; }
      if (!f.host.trim()) { this.$toast('主机不能为空', true); return; }
      this.busy = true;
      try {
        if (f.id) await this.$api('/api/admin/servers/' + f.id, { method: 'PUT', body: f });
        else await this.$api('/api/admin/servers', { method: 'POST', body: f });
        this.$toast('已保存'); this.form = null; await this.load();
      } catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
    async del(s) {
      if (!confirm('删除服务器「' + s.name + '」？')) return;
      try { await this.$api('/api/admin/servers/' + s.id, { method: 'DELETE' }); this.$toast('已删除'); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
    },
    async test(s) {
      this.testing = s.id;
      try { const r = await this.$api('/api/admin/servers/' + s.id + '/test', { method: 'POST' }); this.$toast(r.message || '连接成功'); await this.load(); }
      catch (e) { this.$toast(e.message, true); }
      this.testing = 0;
    },
    sstatus(s) {
      if (s.status === 'online') return { t: '在线', cls: 'green' };
      if (s.status === 'error') return { t: '异常', cls: 'red' };
      return { t: '未知', cls: '' };
    },
    async rebuild(s) {
      if (!confirm('重建「' + s.name + '」的 sing-box 配置？')) return;
      this.rebuilding = s.id;
      try { await this.$api('/api/admin/servers/' + s.id + '/rebuild', { method: 'POST' }); this.$toast(s.name + ' 已重建'); }
      catch (e) { this.$toast(e.message, true); }
      this.rebuilding = 0;
    },
    async startImportRemote(s) {
      this.irLoading = s.id;
      this.ir = null;
      this.irFiles = [];
      this.irPath = '';
      this.irServerId = s.id;
      this.irServerName = s.name;
      try {
        const r = await this.$api('/api/admin/sb/import-remote/list-files?server_id=' + s.id);
        this.irFiles = r.files || [];
        if (this.irFiles.length) this.irPath = this.irFiles[0].path;
      } catch (e) { this.$toast(e.message, true); }
      this.irLoading = 0;
    },
    async previewImportRemote() {
      if (!this.irPath.trim()) { this.$toast('请选择或输入配置文件路径', true); return; }
      this.irLoading = this.irServerId;
      this.ir = null;
      try {
        const r = await this.$api('/api/admin/sb/import-remote/preview?server_id=' + this.irServerId + '&config_path=' + encodeURIComponent(this.irPath.trim()));
        this.ir = r;
      } catch (e) { this.$toast(e.message, true); }
      this.irLoading = 0;
    },
    onFileSelect(e) { this.irPath = e.target.value; },
    closeImportRemote() { this.ir = null; this.irFiles = []; this.irPath = ''; this.irServerId = 0; this.irLoading = -1; },
  },
  template: `
  <div>
    <div class="between mb"><div><h1 class="page-title">服务器管理</h1><div class="muted small">通过 SSH 管理部署节点的服务器</div></div><button class="btn primary" @click="edit(null)">+ 添加服务器</button></div>
    <div class="card">
      <div v-if="!servers.length && !form" class="empty small">还没有服务器，点击右上角添加</div>
      <table v-if="servers.length" class="table"><thead><tr><th>名称</th><th>主机</th><th>SSH 用户</th><th>状态</th><th>启用</th><th></th></tr></thead>
      <tbody><tr v-for="s in servers" :key="s.id">
        <td><b>{{s.name}}</b></td>
        <td class="small dim">{{s.host}}:{{s.port}}</td>
        <td class="small dim">{{s.ssh_user}}</td>
        <td><span class="badge" :class="sstatus(s).cls">{{sstatus(s).t}}</span></td>
        <td><span class="badge" :class="s.enabled?'green':''">{{s.enabled?'启用':'停用'}}</span></td>
        <td><div class="flex"><button class="btn sm" :disabled="testing===s.id" @click="test(s)"><span v-if="testing===s.id" class="spin"></span><span v-else>测试连接</span></button><button class="btn sm" :disabled="rebuilding===s.id" @click="rebuild(s)"><span v-if="rebuilding===s.id" class="spin"></span><span v-else>重建</span></button><button class="btn sm" :disabled="irLoading===s.id" @click="startImportRemote(s)"><span v-if="irLoading===s.id" class="spin"></span><span v-else>导入配置</span></button><button class="btn sm" @click="edit(s)">编辑</button><button class="btn sm danger" @click="del(s)">删除</button></div></td>
      </tr></tbody></table>
    </div>
    <div v-if="irFiles.length || irLoading===irServerId" class="card" style="margin-top:16px;border:2px solid #10b981">
      <div class="between" style="margin-bottom:12px">
        <h3 style="margin:0">导入远程配置 · {{irServerName}}</h3>
        <button class="btn sm" @click="closeImportRemote">关闭</button>
      </div>
      <div v-if="irLoading===irServerId && !irFiles.length" class="dim small" style="padding:8px 0"><span class="spin"></span> 扫描远程文件中…</div>
      <template v-else>
        <div class="field"><label>选择配置文件</label>
          <select class="input" :value="irPath" @change="onFileSelect">
            <option v-for="f in irFiles" :value="f.path">{{f.path}} <template v-if="f.size">({{f.size}}B)</template></option>
          </select>
        </div>
        <div class="field"><label>或手动输入路径</label>
          <input class="input" v-model="irPath" placeholder="/etc/s-box/sb.json">
        </div>
        <button class="btn primary" :disabled="irLoading===irServerId" @click="previewImportRemote"><span v-if="irLoading===irServerId" class="spin"></span><span v-else>预览配置</span></button>
      </template>
    </div>
    <div v-if="ir" class="card" style="margin-top:16px;border:2px solid #10b981">
      <div class="between" style="margin-bottom:12px">
        <h3 style="margin:0">远程配置预览 · {{ir.server_name}}（{{ir.config_path}}）</h3>
        <button class="btn sm" @click="ir=null">关闭预览</button>
      </div>
      <table class="table"><thead><tr><th>类型</th><th>标签</th><th>端口</th></tr></thead>
      <tbody><tr v-for="(ib,i) in ir.inbounds" :key="i">
        <td><span class="badge">{{ib.type}}</span></td>
        <td class="small mono">{{ib.tag}}</td>
        <td class="small">{{ib.listen_port}}</td>
      </tr></tbody></table>
      <div class="small dim" style="padding:8px 0">共 {{ir.inbounds.length}} 个入站协议。目前仅预览，导入功能开发中。</div>
    </div>
    <div v-if="form" class="card" style="margin-top:16px;border:2px solid var(--accent,#6366f1)">
      <h3 style="margin:0 0 16px">{{form.id ? '编辑服务器 — ' + form.name : '添加服务器'}}</h3>
      <div class="field"><label>名称</label><input class="input" v-model="form.name" placeholder="如 日本-东京-01"></div>
      <div class="row">
        <div class="field"><label>主机 IP / 域名</label><input class="input" v-model="form.host" placeholder="1.2.3.4"></div>
        <div class="field" style="flex:0 0 100px"><label>SSH 端口</label><input class="input" type="number" v-model="form.port"></div>
      </div>
      <div class="field"><label>SSH 用户</label><input class="input" v-model="form.ssh_user" placeholder="root"></div>
      <div class="field"><label>SSH 密码（与私钥二选一）</label><input class="input" type="password" v-model="form.ssh_password" placeholder="输入密码"></div>
      <div class="field"><label>SSH 私钥</label><textarea class="mono" style="min-height:80px;font-size:11px" v-model="form.ssh_key" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></div>
      <div class="field"><label>SSH 私钥密码（可选）</label><input class="input" type="password" v-model="form.ssh_key_pass" placeholder="留空表示无密码"></div>
      <div class="field"><label>sing-box 配置路径</label><input class="input" v-model="form.config_path" placeholder="/etc/sing-box/config.json"></div>
      <div class="field"><label>systemd 服务名</label><input class="input" v-model="form.systemd_unit" placeholder="sing-box"></div>
      <div class="row">
        <div class="field"><label>sing-box 二进制路径</label><input class="input" v-model="form.sing_box_bin" placeholder="/usr/local/bin/sing-box"></div>
        <div class="field"><label>v2ray-api 监听地址</label><input class="input" v-model="form.v2ray_listen" placeholder="127.0.0.1:10085"></div>
      </div>
      <label class="flex" style="margin:6px 0 16px"><input type="checkbox" v-model="form.enabled"> <span>启用此服务器</span></label>
      <div class="flex" style="gap:8px">
        <button class="btn primary" :disabled="busy" @click="save"><span v-if="busy" class="spin"></span><span v-else>保存</span></button>
        <button class="btn" @click="cancel">取消</button>
      </div>
    </div>
  </div>`,
});

/* ===== ADMIN MONITOR DASHBOARD ===== */
A.component('view-admin-monitor', {
  data() { return { servers: [], alerts: [], loading: true, editServer: null, expandedId: 0, detailRange: '24h', detailData: null, echLoaded: false, echCharts: {}, showAllIP: false, timer: null }; },
  async mounted() { await this.load(); this.timer = setInterval(() => this.loadQuiet(), 30000); },
  beforeUnmount() { Object.values(this.echCharts).forEach(c => c && c.dispose()); if (this.timer) clearInterval(this.timer); },
  computed: {
    online() { return this.servers.filter(s => this.up(s)).length; },
    expiring() { return this.servers.filter(s => s.days_left !== null && s.days_left <= 7).length; },
  },
  methods: {
    async load() {
      this.loading = true;
      try { const [s, a] = await Promise.all([this.$api('/api/admin/monitor/servers'), this.$api('/api/admin/monitor/alerts?unread=1')]); this.servers = s || []; this.alerts = a || []; } catch (e) { this.$toast(e.message, true); }
      this.loading = false;
    },
    async loadQuiet() { try { const [s, a] = await Promise.all([this.$api('/api/admin/monitor/servers'), this.$api('/api/admin/monitor/alerts?unread=1')]); this.servers = s || []; this.alerts = a || []; } catch (e) {} },
    up(s) { return s.status === 'online' || (s.metrics && s.last_seen && Date.now()/1000 - s.last_seen < 120); },
    pct(u, t) { return t > 0 ? Math.round(u / t * 100) : 0; },
    ago(ts) { if (!ts) return ''; const s = Math.max(0, Math.floor(Date.now()/1000) - ts); if (s < 60) return s + '秒前'; if (s < 3600) return Math.floor(s/60) + '分钟前'; if (s < 86400) return Math.floor(s/3600) + '小时前'; return Math.floor(s/86400) + '天前'; },
    fmtUp(sec) { if (!sec) return '--'; const d = Math.floor(sec/86400), h = Math.floor(sec%86400/3600); return d > 0 ? d + '天' + h + '时' : h + '时' + Math.floor(sec%3600/60) + '分'; },
    sc(s) { return this.up(s) ? '#10b981' : '#ef4444'; },
    maskIP(h) { if (!h || this.showAllIP) return h || '--'; const p = h.split('.'); return p.length === 4 ? p[0]+'.'+p[1]+'.*.**' : h; },
    meta(s) { return [s.provider, s.location, s.spec].filter(Boolean).join(' / '); },
    expText(s) { if (!s.expiry_date) return ''; if (s.days_left !== null && s.days_left <= 0) return '已过期'; return this.$date(s.expiry_date) + (s.days_left !== null ? ' (' + s.days_left + '天)' : ''); },
    barColor(v) { return v > 90 ? 'linear-gradient(90deg,#ef4444,#dc2626)' : v > 75 ? 'linear-gradient(90deg,#f59e0b,#d97706)' : 'linear-gradient(90deg,#10b981,#059669)'; },
    async toggleProbe(s) { try { const r = await this.$api('/api/admin/servers/'+s.id+'/monitor', { method:'PUT', body:{ probe_enabled: !s.probe_enabled } }); s.probe_enabled=r.probe_enabled; s.probe_token=r.probe_token; this.$toast(r.probe_enabled?'已启用':'已停用'); } catch(e) { this.$toast(e.message, true); } },
    async dismissAlert(a) { await this.$api('/api/admin/monitor/alerts/'+a.id+'/read', { method:'POST' }); this.alerts=this.alerts.filter(x=>x.id!==a.id); },
    openEdit(s) { this.editServer = { id:s.id, provider:s.provider||'', location:s.location||'', spec:s.spec||'', price:s.price||0, expiryStr:s.expiry_date?new Date(s.expiry_date*1000).toISOString().slice(0,10):'', notes:s.notes||'' }; },
    async saveMonitor() { const e=this.editServer, body={provider:e.provider,location:e.location,spec:e.spec,price:Number(e.price)||0,notes:e.notes,expiry_date:e.expiryStr?Math.floor(new Date(e.expiryStr).getTime()/1000):0}; try{await this.$api('/api/admin/servers/'+e.id+'/monitor',{method:'PUT',body});this.$toast('已保存');this.editServer=null;await this.load();}catch(err){this.$toast(err.message,true);} },
    copyCmd(s) { this.$copy('bash <(curl -sL '+location.origin+'/api/monitor/install.sh) '+s.probe_token, '已复制'); },
    copyIP(s) { this.$copy(s.host, 'IP已复制'); },
    async expandRow(s) {
      if (this.expandedId===s.id) { this.expandedId=0; return; }
      this.expandedId=s.id; this.detailRange='24h'; this.detailData=null;
      if (!s.probe_enabled||!this.up(s)) return;
      try { const r=await this.$api('/api/admin/monitor/servers/'+s.id+'/metrics?range='+this.detailRange); this.detailData=r?r.data:[]; this.$nextTick(()=>this.renderCharts()); } catch(e) {}
    },
    async switchRange(r) {
      this.detailRange=r;
      try { const res=await this.$api('/api/admin/monitor/servers/'+this.expandedId+'/metrics?range='+r); this.detailData=res?res.data:[]; this.$nextTick(()=>this.renderCharts()); } catch(e) {}
    },
    ensureEcharts() { return new Promise(resolve => { if (window.echarts) { this.echLoaded=true; resolve(); return; } const s=document.createElement('script'); s.src='https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js'; s.onload=()=>{this.echLoaded=true;resolve();}; s.onerror=()=>resolve(); document.head.appendChild(s); }); },
    async renderCharts() {
      await this.ensureEcharts(); if (!window.echarts||!this.detailData||!this.detailData.length) return;
      // Dispose old charts
      Object.values(this.echCharts).forEach(c => c && c.dispose());
      this.echCharts = {};
      const d=this.detailData, t=d.map(x=>new Date(x.ts*1000).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'}));
      const ls={type:'line',smooth:true,symbol:'none',lineStyle:{width:2}};
      const base={ tooltip:{trigger:'axis',backgroundColor:'rgba(255,255,255,0.96)',borderColor:'#ece9e1',borderRadius:8,textStyle:{color:'#322f2a',fontSize:11},shadowBlur:8,shadowColor:'rgba(0,0,0,0.05)'}, grid:{left:40,right:10,top:8,bottom:16}, xAxis:{type:'category',data:t,show:false}, yAxis:{type:'value',axisLabel:{fontSize:9,color:'#9c978d'},splitLine:{lineStyle:{color:'#f1efe8',type:'dashed'}},axisLine:{show:false}}, animation:true, animationDuration:500, animationEasing:'cubicOut' };
      const charts=[
        ['chCpu',{...base,title:{text:'CPU',textStyle:{fontSize:11,color:'var(--text-3)',fontWeight:'500'},left:8,top:2},series:[{...ls,data:d.map(x=>+x.cpu_percent.toFixed(1)),color:'#10b981',areaStyle:{color:{type:'linear',x:0,y:0,x2:0,y2:1,colorStops:[{offset:0,color:'rgba(16,185,129,0.15)'},{offset:1,color:'rgba(16,185,129,0)'}]}}}],yAxis:{...base.yAxis,axisLabel:{...base.yAxis.axisLabel,formatter:'{value}%'}}}],
        ['chMem',{...base,title:{text:'内存',textStyle:{fontSize:11,color:'var(--text-3)',fontWeight:'500'},left:8,top:2},series:[{...ls,data:d.map(x=>+(x.mem_used/1073741824).toFixed(2)),color:'#6366f1',areaStyle:{color:{type:'linear',x:0,y:0,x2:0,y2:1,colorStops:[{offset:0,color:'rgba(99,102,241,0.15)'},{offset:1,color:'rgba(99,102,241,0)'}]}}}],yAxis:{...base.yAxis,axisLabel:{...base.yAxis.axisLabel,formatter:'{value}G'}}}],
        ['chNet',{...base,title:{text:'网络 KB/s',textStyle:{fontSize:11,color:'var(--text-3)',fontWeight:'500'},left:8,top:2},series:[{...ls,name:'收',data:d.map(x=>Math.round(x.net_rx/1024)),color:'#3b82f6',areaStyle:{color:{type:'linear',x:0,y:0,x2:0,y2:1,colorStops:[{offset:0,color:'rgba(59,130,246,0.12)'},{offset:1,color:'rgba(59,130,246,0)'}]}}},{...ls,name:'发',data:d.map(x=>Math.round(x.net_tx/1024)),color:'#f59e0b',areaStyle:{color:{type:'linear',x:0,y:0,x2:0,y2:1,colorStops:[{offset:0,color:'rgba(245,158,11,0.12)'},{offset:1,color:'rgba(245,158,11,0)'}]}}}],legend:{show:false}}],
        ['chLoad',{...base,title:{text:'负载',textStyle:{fontSize:11,color:'var(--text-3)',fontWeight:'500'},left:8,top:2},series:[{...ls,name:'1m',data:d.map(x=>+x.load1.toFixed(2)),color:'#10b981'},{...ls,name:'5m',data:d.map(x=>+x.load5.toFixed(2)),color:'#f59e0b'}]}],
      ];
      for (const [id, opt] of charts) {
        const el=document.getElementById(id);
        if(!el) continue;
        const c=echarts.init(el); c.setOption(opt); this.echCharts[id]=c;
      }
    },
  },
  template: `
  <div>
    <div class="between mb" style="align-items:center">
      <div class="flex" style="gap:10px;align-items:center">
        <h1 class="page-title" style="margin:0;font-size:20px">服务器监控</h1>
        <span class="mo-pill" style="background:#ecfdf5;color:#065f46"><span class="mo-dot on" style="background:#10b981"></span>{{online}}/{{servers.length}} 在线</span>
        <span v-if="expiring" class="mo-pill" style="background:#fffbeb;color:#92400e"><span class="mo-dot" style="background:#f59e0b"></span>{{expiring}} 到期</span>
        <span v-if="alerts.length" class="mo-pill" style="background:#fef2f2;color:#991b1b"><span class="mo-dot mo-pulse" style="background:#ef4444"></span>{{alerts.length}} 告警</span>
      </div>
      <div class="flex" style="gap:6px">
        <button class="mo-btn" @click="showAllIP=!showAllIP">{{showAllIP?'隐藏IP':'显示IP'}}</button>
        <button class="mo-btn" @click="load">刷新</button>
      </div>
    </div>

    <div v-if="alerts.length" style="margin-bottom:10px">
      <div v-for="a in alerts" :key="a.id" style="display:flex;align-items:center;justify-content:space-between;background:linear-gradient(135deg,#fef2f2,#fff1f2);border:1px solid #fecdd3;border-radius:10px;padding:8px 14px;margin-bottom:4px">
        <span style="font-size:12px;color:#9f1239"><span class="mo-pill" style="background:#fee2e2;color:#b91c1c;font-size:9px;padding:1px 6px">{{a.type}}</span> {{a.message}}</span>
        <button style="background:none;border:none;cursor:pointer;color:#e11d48;font-size:14px;opacity:.5" @click="dismissAlert(a)">&times;</button>
      </div>
    </div>

    <div v-if="loading && !servers.length" class="center" style="padding:50px"><span class="spin"></span></div>

    <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:12px">
      <div v-for="s in servers" :key="s.id" class="mo-card" :style="{'border-top':'3px solid '+sc(s)}">
        <div style="padding:16px 16px 14px;cursor:pointer" @click="expandRow(s)">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:10px">
            <div class="flex" style="align-items:center;gap:8px">
              <span class="mo-dot" :class="up(s)?'on':'off'" :style="{background:sc(s)}"></span>
              <span style="font-size:15px;font-weight:700;color:var(--text)">{{s.name}}</span>
            </div>
            <span class="mo-pill" :style="{background:up(s)?'#ecfdf5':'#fef2f2',color:up(s)?'#065f46':'#991b1b'}">{{up(s)?'在线':'离线'}}</span>
          </div>
          <div style="font-size:12px;color:var(--text-2);margin-bottom:2px"><span style="font-family:monospace" @click.stop="copyIP(s)" title="点击复制">{{maskIP(s.host)}}</span></div>
          <div v-if="meta(s)" style="font-size:11px;color:var(--text-3);margin-bottom:2px">{{meta(s)}}</div>
          <div style="font-size:11px;color:var(--text-3)">
            <span v-if="s.expiry_date" :style="{color:s.days_left<=3?'#ef4444':s.days_left<=7?'#f59e0b':'inherit'}">{{expText(s)}}</span>
            <span v-if="s.price" style="margin-left:8px">月费 &yen;{{s.price}}</span>
          </div>
        </div>
        <div class="mo-sep"></div>
        <div v-if="s.probe_enabled && s.metrics" class="mo-body">
          <div class="mo-grid" style="margin-bottom:12px">
            <div>
              <div style="display:flex;justify-content:space-between;font-size:11px;margin-bottom:4px"><span style="color:var(--text-3)">CPU</span><span style="font-weight:700;font-size:13px;color:var(--text)">{{s.metrics.cpu_percent.toFixed(1)}}%</span></div>
              <div class="mo-bar"><div class="mo-bar-fill" :style="{width:pct(s.metrics.cpu_percent,100)+'%',background:barColor(pct(s.metrics.cpu_percent,100))}"></div></div>
            </div>
            <div>
              <div style="display:flex;justify-content:space-between;font-size:11px;margin-bottom:4px"><span style="color:var(--text-3)">内存</span><span style="font-weight:700;font-size:13px;color:var(--text)">{{pct(s.metrics.mem_used,s.metrics.mem_total)}}%</span></div>
              <div class="mo-bar"><div class="mo-bar-fill" :style="{width:pct(s.metrics.mem_used,s.metrics.mem_total)+'%',background:barColor(pct(s.metrics.mem_used,s.metrics.mem_total))}"></div></div>
              <div style="font-size:10px;color:var(--text-3);margin-top:3px">{{$fmt(s.metrics.mem_used)}} / {{$fmt(s.metrics.mem_total)}}</div>
            </div>
            <div>
              <div style="display:flex;justify-content:space-between;font-size:11px;margin-bottom:4px"><span style="color:var(--text-3)">磁盘</span><span style="font-weight:700;font-size:13px;color:var(--text)">{{pct(s.metrics.disk_used,s.metrics.disk_total)}}%</span></div>
              <div class="mo-bar"><div class="mo-bar-fill" :style="{width:pct(s.metrics.disk_used,s.metrics.disk_total)+'%',background:barColor(pct(s.metrics.disk_used,s.metrics.disk_total))}"></div></div>
              <div style="font-size:10px;color:var(--text-3);margin-top:3px">{{$fmt(s.metrics.disk_used)}} / {{$fmt(s.metrics.disk_total)}}</div>
            </div>
          </div>
          <div class="mo-info">
            <span>网络 <b>&uarr;{{$fmt(s.metrics.net_tx)}}/s</b> <b>&darr;{{$fmt(s.metrics.net_rx)}}/s</b></span>
            <span>负载 <b>{{s.metrics.load1.toFixed(2)}}</b></span>
            <span>进程 <b>{{s.metrics.process_count}}</b></span>
            <span>TCP <b>{{s.metrics.tcp_connections}}</b></span>
            <span>运行 <b>{{fmtUp(s.metrics.uptime)}}</b></span>
          </div>
        </div>
        <div v-else-if="s.probe_enabled" class="mo-body" style="text-align:center;font-size:12px;color:var(--text-3)">等待数据上报 {{ago(s.last_seen)}}</div>
        <div v-else class="mo-body" style="text-align:center;font-size:12px;color:var(--text-3)">未启用探针</div>
        <div class="mo-acts">
          <button class="mo-btn" :class="s.probe_enabled?'mo-btn-d':'mo-btn-p'" @click.stop="toggleProbe(s)">{{s.probe_enabled?'停用探针':'启用探针'}}</button>
          <button class="mo-btn" @click.stop="openEdit(s)">编辑</button>
          <button v-if="s.probe_token" class="mo-btn" @click.stop="copyCmd(s)">复制安装命令</button>
          <button v-if="s.probe_enabled" class="mo-btn" style="margin-left:auto" @click.stop="expandRow(s)">{{expandedId===s.id?'收起':'图表'}}</button>
        </div>

        <!-- Expanded charts -->
        <div v-if="expandedId===s.id" class="mo-exp">
          <template v-if="s.probe_enabled && (up(s)||s.metrics)">
            <div class="flex" style="gap:4px;margin-bottom:10px">
              <div style="display:flex;gap:2px;background:var(--bg);border-radius:8px;padding:3px">
                <button v-for="r in ['1h','6h','24h','7d','30d']" :key="r" style="border:none;cursor:pointer;padding:4px 12px;border-radius:6px;font-size:11px;transition:all .2s" :style="{background:detailRange===r?'var(--accent)':'transparent',color:detailRange===r?'#fff':'var(--text-2)'}" @click="switchRange(r)">{{r}}</button>
              </div>
            </div>
            <div v-if="!detailData" class="center" style="padding:20px"><span class="spin"></span></div>
            <div v-else-if="!detailData.length" style="text-align:center;padding:20px;color:var(--text-3);font-size:12px">暂无数据</div>
            <template v-else>
              <!-- 4 charts in 2x2 grid -->
              <div style="display:grid;grid-template-columns:1fr 1fr;gap:6px;margin-bottom:8px">
                <div style="height:120px;background:var(--bg-soft);border-radius:8px;position:relative" :id="'chCpu'+s.id"></div>
                <div style="height:120px;background:var(--bg-soft);border-radius:8px;position:relative" :id="'chMem'+s.id"></div>
                <div style="height:120px;background:var(--bg-soft);border-radius:8px;position:relative" :id="'chNet'+s.id"></div>
                <div style="height:120px;background:var(--bg-soft);border-radius:8px;position:relative" :id="'chLoad'+s.id"></div>
              </div>
              <!-- System info chips -->
              <div class="mo-info" style="margin-top:6px">
                <span v-if="s.metrics.platform">{{s.metrics.platform}}</span>
                <span v-if="s.metrics.kernel">内核 {{s.metrics.kernel}}</span>
                <span>架构 {{s.metrics.arch}}</span>
                <span>负载 {{s.metrics.load1.toFixed(2)}}/{{s.metrics.load5.toFixed(2)}}/{{s.metrics.load15.toFixed(2)}}</span>
              </div>
            </template>
          </template>
          <div v-else-if="!s.probe_enabled" style="text-align:center;padding:14px">
            <div style="font-size:12px;color:var(--text-2);margin-bottom:8px">启用探针后在目标服务器执行安装命令</div>
            <button class="mo-btn mo-btn-p" @click.stop="toggleProbe(s)">启用探针</button>
          </div>
          <div v-else style="text-align:center;padding:14px">
            <div style="font-size:12px;color:var(--text-2);margin-bottom:8px">离线 · 请确认探针已安装并运行</div>
            <button v-if="s.probe_token" class="mo-btn mo-btn-p" @click.stop="copyCmd(s)">复制安装命令</button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="!loading && !servers.length" style="text-align:center;padding:50px;color:var(--text-3);font-size:13px">请先在「服务器管理」中添加服务器</div>

    <modal v-if="editServer" title="编辑资产信息" @close="editServer=null">
      <div class="row"><div class="field"><label>供应商</label><input class="input" v-model="editServer.provider" placeholder="如 DMIT"></div><div class="field"><label>位置</label><input class="input" v-model="editServer.location" placeholder="如 美国洛杉矶"></div></div>
      <div class="row"><div class="field"><label>规格</label><input class="input" v-model="editServer.spec" placeholder="如 1C1G20G"></div><div class="field"><label>月费</label><input class="input" type="number" step="0.01" v-model.number="editServer.price"></div></div>
      <div class="field"><label>到期</label><input class="input" type="date" v-model="editServer.expiryStr"></div>
      <div class="field"><label>备注</label><input class="input" v-model="editServer.notes"></div>
      <button class="btn primary block" @click="saveMonitor">保存</button>
    </modal>
  </div>`,
});})();







