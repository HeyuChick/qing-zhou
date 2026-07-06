(function () {
const { app, store } = window.qz;

function copy(text, msg) { navigator.clipboard.writeText(text).then(() => window.qz.toast(msg || '已复制')); }
app.config.globalProperties.$copy = copy;
app.config.globalProperties.$gb = (b) => (b / 1073741824);

/* ============ AUTH ============ */
app.component('view-auth', {
  data() { return { mode: 'login', f: { username: '', password: '', email: '', code: '' }, busy: false, msg: '' }; },
  template: `
  <div class="auth"><div class="auth-card">
    <div class="auth-brand"><span class="logo" style="width:34px;height:34px;border-radius:10px;background:linear-gradient(135deg,var(--accent),var(--accent-strong));display:grid;place-items:center;color:#fff">舟</span>轻舟</div>
    <div class="muted small">轻量 · 清爽的订阅管理</div>
    <div class="auth-tabs">
      <button :class="{on:mode==='login'}" @click="mode='login'">登录</button>
      <button :class="{on:mode==='register'}" @click="mode='register'" v-if="store.cfg.register_mode!=='closed'">注册</button>
      <button :class="{on:mode==='forgot'}" @click="mode='forgot'">找回</button>
    </div>
    <form @submit.prevent="submit">
      <template v-if="mode!=='forgot'">
        <div class="field"><label>用户名</label><input class="input" v-model="f.username" placeholder="用户名" autocomplete="username"></div>
        <div class="field" v-if="mode==='register'"><label>邮箱{{store.cfg.email_verify_required?'（需验证）':'（可选）'}}</label><input class="input" v-model="f.email" placeholder="you@example.com" autocomplete="email"></div>
        <div class="field" v-if="mode==='register' && store.cfg.register_mode==='code'"><label>注册码</label><input class="input" v-model="f.code" placeholder="请输入注册码"></div>
        <div class="field"><label>密码</label><input class="input" type="password" v-model="f.password" placeholder="密码" autocomplete="current-password"></div>
      </template>
      <div class="field" v-else><label>注册邮箱</label><input class="input" v-model="f.email" placeholder="you@example.com"></div>
      <div v-if="msg" class="small" style="color:var(--danger);margin-bottom:10px">{{msg}}</div>
      <button class="btn primary block" :disabled="busy"><span v-if="busy" class="spin"></span><span v-else>{{ {login:'登录',register:'注册',forgot:'发送重置邮件'}[mode] }}</span></button>
    </form>
  </div></div>`,
  methods: {
    async submit() {
      this.busy = true; this.msg = '';
      try {
        if (this.mode === 'login') {
          const d = await this.$api('/api/auth/login', { method: 'POST', body: { username: this.f.username, password: this.f.password } });
          store.token = d.token; localStorage.setItem('qz_token', d.token); store.user = d.user; window.qz.loadNotices();
          location.hash = d.user.role === 'admin' ? '/admin' : '/';
        } else if (this.mode === 'register') {
          const d = await this.$api('/api/auth/register', { method: 'POST', body: this.f });
          if (d.need_verify) { this.msg = d.message; this.mode = 'login'; this.$toast('请查收验证邮件'); }
          else { store.token = d.token; localStorage.setItem('qz_token', d.token); store.user = d.user; window.qz.loadNotices(); location.hash = '/'; }
        } else {
          await this.$api('/api/auth/forgot', { method: 'POST', body: { email: this.f.email } });
          this.$toast('若邮箱已注册，重置邮件已发送'); this.mode = 'login';
        }
      } catch (e) { this.msg = e.message; }
      this.busy = false;
    },
  },
});

/* ============ USER: DASHBOARD ============ */
app.component('view-dashboard', {
  data() { return { d: null, trend: [], range: '7d', loading: true }; },
  async mounted() { await this.load(); },
  computed: {
    pct() { if (!this.d || !this.d.traffic.total) return 0; return this.d.traffic.used / this.d.traffic.total * 100; },
    dleft() { return this.$days(this.d && this.d.expiry_at); },
    notices() { return store.notices || []; },
    warn() {
      const d = this.d; if (!d) return '';
      if (d.status === 'banned') return '账号已被封禁，请联系管理员';
      if (d.expiry_at && d.expiry_at * 1000 < Date.now()) return '订阅已过期，请续费后继续使用';
      if (d.traffic.total > 0 && d.traffic.used >= d.traffic.total) return '流量已用尽，请购买流量包或续费';
      return '';
    },
  },
  methods: {
    async load() { this.loading = true; this.d = await this.$api('/api/user/dashboard'); await this.loadTrend(); this.loading = false; },
    async loadTrend() { this.trend = await this.$api('/api/user/stats/traffic?range=' + this.range) || []; },
    setRange(r) { this.range = r; this.loadTrend(); },
  },
  template: `
  <div v-if="loading" class="center"><span class="spin"></span></div>
  <div v-else>
    <h1 class="page-title">你好，{{store.user.username}}</h1>
    <div class="page-sub">这是你的账户概览</div>
    <div v-if="warn" class="card mb" style="border-left:3px solid var(--danger);background:var(--danger-soft)">
      <div class="flex" style="gap:10px"><span style="font-size:18px">⚠</span><b>{{warn}}</b><a class="btn sm" style="margin-left:auto" @click="$go('/shop')">去续费</a></div>
    </div>
    <div class="card mb" v-if="notices.length" style="border-left:3px solid var(--accent)">
      <div class="card-h"><h3>📢 公告</h3></div>
      <div v-for="(n,i) in notices" :key="n.id" :style="i?'border-top:1px solid var(--border-soft);padding-top:12px;margin-top:12px':''">
        <div class="flex" style="gap:8px"><b>{{n.title}}</b><span v-if="n.pinned" class="badge amber">置顶</span></div>
        <div class="small muted" style="white-space:pre-wrap;margin-top:4px">{{n.content}}</div>
        <div class="small dim" style="margin-top:4px">{{$date(n.created_at)}}</div>
      </div>
    </div>
    <div class="grid cols-3 mb">
      <div class="card stat"><div class="tile-ic">⇅</div><div class="label">剩余流量</div><div class="value">{{$fmt(d.traffic.remaining)}}</div><div class="sub">共 {{$total(d.traffic.total)}}</div></div>
      <div class="card stat"><div class="tile-ic">◷</div><div class="label">到期</div><div class="value">{{dleft===null?'永久':dleft+' 天'}}</div><div class="sub">{{$date(d.expiry_at)}}</div></div>
      <div class="card stat"><div class="tile-ic">◈</div><div class="label">积分</div><div class="value">{{d.points}}</div><div class="sub">{{$yuan(d.points)}}</div></div>
    </div>
    <div class="grid cols-2 mb">
      <div class="card">
        <div class="card-h"><h3>流量使用</h3><span class="badge" :class="pct>90?'red':pct>70?'amber':'green'">{{pct.toFixed(0)}}%</span></div>
        <div class="ring-wrap">
          <div style="position:relative">
            <ring :percent="pct" :color="pct>90?'var(--danger)':'var(--accent)'"/>
            <div style="position:absolute;inset:0;display:grid;place-items:center;text-align:center">
              <div><div style="font-size:18px;font-weight:700">{{$fmt(d.traffic.used)}}</div><div class="small dim">已用</div></div>
            </div>
          </div>
          <div style="flex:1">
            <div class="between small"><span class="muted">已用</span><b>{{$fmt(d.traffic.used)}}</b></div>
            <div class="between small mt"><span class="muted">剩余</span><b>{{$fmt(d.traffic.remaining)}}</b></div>
            <div class="between small mt"><span class="muted">总量</span><b>{{$total(d.traffic.total)}}</b></div>
          </div>
        </div>
      </div>
      <div class="card">
        <div class="card-h"><h3>流量趋势</h3><div class="flex"><span class="chip" :class="{on:range==='7d'}" @click="setRange('7d')">7天</span><span class="chip" :class="{on:range==='30d'}" @click="setRange('30d')">30天</span></div></div>
        <bars :data="trend"/>
        <div class="flex small dim mt" style="gap:16px"><span><i style="display:inline-block;width:9px;height:9px;border-radius:3px;background:var(--accent)"></i> 下行</span><span><i style="display:inline-block;width:9px;height:9px;border-radius:3px;background:#bcd2c1"></i> 上行</span></div>
      </div>
    </div>
    <div class="card">
      <div class="card-h"><h3>快速订阅</h3><a class="btn sm" @click="$go('/sub')">详情 →</a></div>
      <div class="copybox"><code>{{d.subscription_url}}</code><button class="btn sm primary" @click="$copy(d.subscription_url,'订阅链接已复制')">复制</button></div>
    </div>
  </div>`,
});

/* ============ USER: ACCOUNT ============ */
app.component('view-account', {
  data() { return { sessions: [], pw: { old_password: '', new_password: '', confirm: '' }, busy: false, resending: false, sessPage: 1, sessSize: 5, emailEdit: false, emailInput: '', emailBusy: false }; },
  async mounted() { await this.refreshMe(); await this.loadSessions(); },
  computed: {
    me() { return store.user || {}; },
    sortedSessions() { return [...this.sessions].sort((a, b) => (b.current === true) - (a.current === true) || (b.last_seen || 0) - (a.last_seen || 0)); },
    sessPages() { return Math.max(1, Math.ceil(this.sessions.length / this.sessSize)); },
    pagedSessions() { const s = (this.sessPage - 1) * this.sessSize; return this.sortedSessions.slice(s, s + this.sessSize); },
  },
  methods: {
    async loadSessions() { this.sessions = await this.$api('/api/user/sessions') || []; if (this.sessPage > this.sessPages) this.sessPage = this.sessPages; },
    async refreshMe() { try { const u = await this.$api('/api/auth/me'); if (u) store.user = u; } catch (e) {} },
    async resendVerify() {
      this.resending = true;
      try { const r = await this.$api('/api/user/resend-verify', { method: 'POST' }); this.$toast((r && r.message) || '验证邮件已发送'); }
      catch (e) { this.$toast(e.message, true); }
      this.resending = false;
    },
    startEditEmail() { this.emailInput = this.me.email || ''; this.emailEdit = true; },
    async saveEmail() {
      const e = (this.emailInput || '').trim();
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(e)) { this.$toast('邮箱格式不正确', true); return; }
      this.emailBusy = true;
      try { const r = await this.$api('/api/user/email', { method: 'POST', body: { email: e } }); this.$toast((r && r.message) || '验证邮件已发送'); this.emailEdit = false; await this.refreshMe(); }
      catch (err) { this.$toast(err.message, true); }
      this.emailBusy = false;
    },
    uaLabel(ua) {
      ua = ua || '';
      let os = '未知设备';
      if (/Windows/i.test(ua)) os = 'Windows'; else if (/iPhone|iPad|iOS/i.test(ua)) os = 'iOS'; else if (/Android/i.test(ua)) os = 'Android'; else if (/Mac OS X|Macintosh/i.test(ua)) os = 'macOS'; else if (/Linux/i.test(ua)) os = 'Linux';
      let br = ''; if (/Edg/i.test(ua)) br = 'Edge'; else if (/Firefox/i.test(ua)) br = 'Firefox'; else if (/Chrome/i.test(ua)) br = 'Chrome'; else if (/Safari/i.test(ua)) br = 'Safari';
      return os + (br ? ' · ' + br : '');
    },
    async revoke(s) { if (!confirm('注销该设备的登录？')) return; await this.$api('/api/user/sessions/' + s.id + '/revoke', { method: 'POST' }); await this.loadSessions(); this.$toast('已注销'); },
    async changePw() {
      if (this.pw.new_password.length < 6) { this.$toast('新密码至少 6 位', true); return; }
      if (this.pw.new_password !== this.pw.confirm) { this.$toast('两次输入的新密码不一致', true); return; }
      this.busy = true;
      try { await this.$api('/api/user/password', { method: 'POST', body: { old_password: this.pw.old_password, new_password: this.pw.new_password } }); this.$toast('密码已修改'); this.pw = { old_password: '', new_password: '', confirm: '' }; await this.loadSessions(); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = false;
    },
  },
  template: `
  <div>
    <h1 class="page-title">个人中心</h1><div class="page-sub">个人信息、邮箱验证、密码与登录设备</div>
    <div class="card mb">
      <div class="card-h"><h3>账户信息</h3></div>
      <div class="between" style="padding:8px 0"><span class="dim">用户名</span><b>{{me.username}}</b></div>
      <div class="between" style="padding:8px 0;border-top:1px solid var(--border-soft)">
        <span class="dim">邮箱</span>
        <span class="flex" style="gap:8px">
          <span>{{me.email||'未绑定'}}</span>
          <span v-if="me.email && me.email_verified" class="badge green">已验证</span>
          <span v-else-if="me.email" class="badge amber">未验证</span>
        </span>
      </div>
      <div v-if="!emailEdit" class="flex wrap mt" style="gap:8px">
        <button class="btn sm" @click="startEditEmail">{{me.email?'修改邮箱':'绑定邮箱'}}</button>
        <button v-if="me.email && !me.email_verified" class="btn sm primary" :disabled="resending" @click="resendVerify"><span v-if="resending" class="spin"></span><span v-else>重新发送验证邮件</span></button>
        <button v-if="me.email && !me.email_verified" class="btn sm" @click="refreshMe">我已验证 · 刷新</button>
      </div>
      <div v-else class="mt">
        <div class="field" style="margin-bottom:10px"><label>邮箱地址</label><input class="input" type="email" v-model="emailInput" placeholder="you@example.com" @keyup.enter="saveEmail"></div>
        <div class="flex" style="gap:8px">
          <button class="btn sm primary" :disabled="emailBusy" @click="saveEmail"><span v-if="emailBusy" class="spin"></span><span v-else>发送验证链接</span></button>
          <button class="btn sm" @click="emailEdit=false">取消</button>
        </div>
        <div class="small dim mt">提交后将向该邮箱发送验证链接，点击链接即完成绑定。</div>
      </div>
      <div v-if="me.email && !me.email_verified && !emailEdit" class="small dim mt">收到验证邮件后点击其中链接即可（记得看垃圾箱）；验证后回到本页点"刷新"。</div>
    </div>
    <div class="grid cols-2 mb">
      <div class="card">
        <div class="card-h"><h3>修改密码</h3></div>
        <div class="field"><label>当前密码</label><input class="input" type="password" v-model="pw.old_password"></div>
        <div class="field"><label>新密码</label><input class="input" type="password" v-model="pw.new_password" placeholder="至少 6 位"></div>
        <div class="field"><label>确认新密码</label><input class="input" type="password" v-model="pw.confirm"></div>
        <button class="btn primary block" :disabled="busy" @click="changePw"><span v-if="busy" class="spin"></span><span v-else>修改密码</span></button>
        <div class="small dim mt">修改后其他设备会被退出登录。</div>
      </div>
      <div class="card">
        <div class="card-h"><h3>登录设备</h3><span class="small dim">{{sessions.length}} 台在线</span></div>
        <div v-if="!sessions.length" class="empty small">暂无登录记录</div>
        <div v-for="s in pagedSessions" :key="s.id" class="between" style="padding:9px 0;border-bottom:1px solid var(--border-soft)">
          <div><div>{{uaLabel(s.user_agent)}} <span v-if="s.current" class="badge green">本机</span></div><div class="small dim">{{s.ip||'—'}} · {{$dt(s.last_seen)}}</div></div>
          <button v-if="!s.current" class="btn sm danger" @click="revoke(s)">注销</button>
        </div>
        <div v-if="sessPages>1" class="between mt">
          <button class="btn sm" :disabled="sessPage<=1" @click="sessPage--">上一页</button>
          <span class="small dim">第 {{sessPage}} / {{sessPages}} 页</span>
          <button class="btn sm" :disabled="sessPage>=sessPages" @click="sessPage++">下一页</button>
        </div>
      </div>
    </div>
  </div>`,
});

/* ============ USER: HELP ============ */
app.component('view-help', {
  data() { return { docs: [], current: null, loading: true }; },
  async mounted() { this.docs = await this.$api('/api/help') || []; this.current = this.docs[0] || null; this.loading = false; },
  template: `
  <div>
    <h1 class="page-title">帮助中心</h1><div class="page-sub">使用说明与常见问题</div>
    <div v-if="loading" class="center"><span class="spin"></span></div>
    <div v-else-if="!docs.length" class="empty">暂无帮助文档</div>
    <div v-else class="help-layout">
      <aside class="help-nav card">
        <div class="help-nav-t">文档目录</div>
        <div v-for="d in docs" :key="d.id" class="help-nav-item" :class="{on:current&&current.id===d.id}" @click="current=d">{{d.title}}</div>
      </aside>
      <div class="card help-doc" v-if="current">
        <h2 class="help-doc-t">{{current.title}}</h2>
        <div class="md" v-html="$md(current.content)"></div>
      </div>
    </div>
  </div>`,
});

/* ============ USER: SUBSCRIPTION ============ */
app.component('view-sub', {
  data() { return { d: null, dash: null, plans: [], fmt: 'default', nodes: [], tested: false, pinging: false, reloading: false, q: '', proto: '', group: '', latMin: '', latMax: '', page: 1, pageSize: 20, showDisabled: false, busyAll: false, condMin: 0, condMax: 300, sel: {} }; },
  async mounted() { this.d = await this.$api('/api/user/subscription'); this.dash = await this.$api('/api/user/dashboard'); this.plans = await this.$api('/api/user/plans') || []; await this.reloadNodes(); },
  computed: {
    url() { if (!this.d) return ''; return this.fmt === 'default' ? this.d.url : this.d.formats[this.fmt]; },
    pct() { const t = this.dash; if (!t || !t.traffic.total) return 0; return Math.min(100, t.traffic.used / t.traffic.total * 100); },
    dleft() { return this.$days(this.dash && this.dash.expiry_at); },
    protocols() { const s = new Set(); this.nodes.forEach(n => n.protocol && s.add(n.protocol)); return [...s].sort(); },
    groupNames() { const s = new Set(); this.nodes.forEach(n => n.group && s.add(n.group)); return [...s].sort(); },
    disabledCount() { return this.nodes.filter(n => n.disabled).length; },
    filteredNodes() {
      const q = this.q.trim().toLowerCase();
      const lo = this.latMin === '' ? null : Number(this.latMin), hi = this.latMax === '' ? null : Number(this.latMax);
      const latActive = lo !== null || hi !== null;
      return this.nodes.filter(n => {
        if (!this.showDisabled && n.disabled) return false;
        if (this.proto && n.protocol !== this.proto) return false;
        if (this.group && n.group !== this.group) return false;
        if (latActive) {
          if (!n.ok) return false; // 无测速结果（超时/UDP/未测）在延迟筛选下隐藏
          if (lo !== null && n.latency < lo) return false;
          if (hi !== null && n.latency > hi) return false;
        }
        if (!q) return true;
        return (this.nodeName(n.name) || '').toLowerCase().includes(q) || (n.server || '').toLowerCase().includes(q);
      });
    },
    totalPages() { return Math.max(1, Math.ceil(this.filteredNodes.length / this.pageSize)); },
    pagedNodes() { const p = Math.min(this.page, this.totalPages); return this.filteredNodes.slice((p - 1) * this.pageSize, p * this.pageSize); },
    selectedKeys() { return Object.keys(this.sel).filter(k => this.sel[k]); },
    filteredAllSelected() { return this.filteredNodes.length > 0 && this.filteredNodes.every(n => this.sel[n.key]); },
  },
  watch: { q() { this.page = 1; }, proto() { this.page = 1; }, group() { this.page = 1; }, latMin() { this.page = 1; }, latMax() { this.page = 1; }, showDisabled() { this.page = 1; } },
  methods: {
    async reset() { if (!confirm('重置后旧链接立即失效，需在客户端重新导入。继续？')) return; await this.$api('/api/user/reset-sub', { method: 'POST' }); this.d = await this.$api('/api/user/subscription'); this.$toast('订阅链接已重置'); },
    async reloadNodes() { this.reloading = true; try { this.nodes = await this.$api('/api/user/nodes') || []; this.tested = false; this.sel = {}; } catch (e) { this.$toast(e.message, true); } this.reloading = false; },
    async testSpeed() { this.pinging = true; try { this.nodes = await this.$api('/api/user/nodes/ping') || []; this.tested = true; this.$toast('测速完成，共 ' + this.nodes.length + ' 个节点'); } catch (e) { this.$toast(e.message, true); } this.pinging = false; },
    async toggleNode(n) {
      const next = !n.disabled;
      try { await this.$api('/api/user/nodes/toggle', { method: 'POST', body: { key: n.key, disabled: next } }); n.disabled = next; }
      catch (e) { this.$toast(e.message, true); }
    },
    async disableAll() {
      if (!confirm('禁用当前订阅里的全部 ' + this.nodes.length + ' 个节点？\n仅对你自己生效，之后可逐个重新启用。')) return;
      this.busyAll = true;
      try { const r = await this.$api('/api/user/nodes/disable-all', { method: 'POST' }); this.$toast('已禁用 ' + r.disabled + ' 个节点'); this.showDisabled = true; await this.reloadNodes(); }
      catch (e) { this.$toast(e.message, true); }
      this.busyAll = false;
    },
    async enableAll() {
      this.busyAll = true;
      try { await this.$api('/api/user/nodes/enable-all', { method: 'POST' }); this.$toast('已启用全部节点'); await this.reloadNodes(); }
      catch (e) { this.$toast(e.message, true); }
      this.busyAll = false;
    },
    toggleSel(n) { this.sel[n.key] = !this.sel[n.key]; },
    toggleAllFiltered() { const target = !this.filteredAllSelected; this.filteredNodes.forEach(n => { this.sel[n.key] = target; }); },
    clearSel() { this.sel = {}; },
    async bulkKeys(keys, disabled) {
      if (!keys.length) { this.$toast('没有可操作的节点', true); return; }
      this.busyAll = true;
      const body = disabled ? { disable: keys, enable: [] } : { enable: keys, disable: [] };
      try { const r = await this.$api('/api/user/nodes/bulk', { method: 'POST', body }); this.$toast((disabled ? '禁用 ' : '启用 ') + (disabled ? r.disabled : r.enabled) + ' 个'); this.clearSel(); await this.reloadNodes(); }
      catch (e) { this.$toast(e.message, true); }
      this.busyAll = false;
    },
    bulkSelected(disabled) { this.bulkKeys(this.selectedKeys, disabled); },
    bulkFiltered(disabled) {
      const keys = this.filteredNodes.map(n => n.key);
      const label = this.group ? ('分组「' + this.group + '」') : '当前筛选结果';
      if (!confirm((disabled ? '禁用' : '启用') + label + '的 ' + keys.length + ' 个节点？')) return;
      this.bulkKeys(keys, disabled);
    },
    async applyCond() {
      if (!this.tested) { this.$toast('请先点「测速」获取延迟', true); return; }
      const min = Number(this.condMin) || 0, max = Number(this.condMax) || 0;
      if (max <= 0 || max < min) { this.$toast('请填写有效的延迟区间', true); return; }
      const enable = [], disable = [];
      for (const n of this.nodes) {
        if (n.udp) continue; // UDP 无法服务端测速，保持原状
        if (n.ok && n.latency >= min && n.latency <= max) enable.push(n.key);
        else disable.push(n.key); // 超时或超出区间
      }
      if (!enable.length && !confirm('该区间内没有可用节点，将禁用全部可测速节点。继续？')) return;
      this.busyAll = true;
      try { const r = await this.$api('/api/user/nodes/bulk', { method: 'POST', body: { enable, disable } }); this.$toast('启用 ' + r.enabled + ' 个，禁用 ' + r.disabled + ' 个'); await this.reloadNodes(); }
      catch (e) { this.$toast(e.message, true); }
      this.busyAll = false;
    },
    nodeName(s) { return (s || '').replace(/\s+[\d.]+\s*[KMGTP]?B[^\s]*.*$/u, '').trim() || s; },
    planPct(p) { return p.traffic_limit > 0 ? Math.min(100, p.used / p.traffic_limit * 100) : 0; },
    planBadge(p) { if (p.kind === 'pool') return { t: '通用流量', c: 'blue' }; if (p.status === 'expired') return { t: '已到期', c: '' }; if (p.status === 'exhausted') return { t: '已用尽', c: 'amber' }; return { t: '生效中', c: 'green' }; },
  },
  template: `
  <div v-if="d && dash">
    <h1 class="page-title">我的订阅</h1><div class="page-sub">你的套餐、资源与到期，以及订阅导入</div>

    <div class="card mb">
      <div class="card-h"><h3>资源总览（全部套餐合计）</h3><span class="badge" :class="dash.current_plan?'green':''">{{dash.current_plan||'基础（无套餐）'}}</span></div>
      <div class="grid cols-2" style="gap:20px">
        <div>
          <div class="small muted mb">流量</div>
          <div class="bar-track mb"><div class="bar-fill" :style="{width:pct+'%'}"></div></div>
          <div class="small">已用 <b>{{$fmt(dash.traffic.used)}}</b> / {{$total(dash.traffic.total)}}</div>
          <div class="small dim">剩余 {{$fmt(dash.traffic.remaining)}}</div>
        </div>
        <div>
          <div class="small muted mb">到期时间</div>
          <div style="font-size:22px;font-weight:720">{{dleft===null?'永久':dleft+' 天'}}</div>
          <div class="small dim">{{$date(dash.expiry_at)}}</div>
        </div>
      </div>
      <div class="divider"></div>
      <div class="flex wrap" style="gap:18px">
        <a class="small" style="cursor:pointer" @click="$go('/shop')">→ 去商城续费 / 加量</a>
        <a class="small" style="cursor:pointer" @click="$go('/orders')">→ 查看购买记录</a>
      </div>
    </div>

    <div class="card mb" v-if="plans.length">
      <div class="card-h"><h3>各套餐资源（独立计费）</h3><span class="small dim">{{plans.length}} 项</span></div>
      <div v-for="p in plans" :key="p.id" style="padding:11px 0;border-bottom:1px solid var(--border-soft)">
        <div class="between mb">
          <span style="white-space:nowrap"><b>{{p.name}}</b> <span class="badge" :class="planBadge(p).c">{{planBadge(p).t}}</span></span>
          <span class="small dim" style="white-space:nowrap" v-if="p.kind!=='pool'">{{p.expiry_at?($days(p.expiry_at)+' 天后到期'):'永久'}}</span>
        </div>
        <div class="bar-track mb"><div class="bar-fill" :style="{width:planPct(p)+'%'}"></div></div>
        <div class="small">已用 <b>{{$fmt(p.used)}}</b> / {{p.remaining<0?'不限':$total(p.traffic_limit)}}<span v-if="p.remaining>=0" class="dim"> · 剩余 {{$fmt(p.remaining)}}</span></div>
      </div>
      <div class="small dim mt">每个套餐的流量与时间各自独立结算、互不影响：某套餐用尽或到期，只下线它名下的节点，其它套餐照常使用。</div>
    </div>

    <div class="grid cols-2">
      <div class="card">
        <div class="card-h"><h3>订阅链接</h3></div>
        <div class="flex wrap mb">
          <span class="chip" :class="{on:fmt==='default'}" @click="fmt='default'">通用 (base64)</span>
          <span class="chip" :class="{on:fmt==='clash'}" @click="fmt='clash'">Clash</span>
          <span class="chip" :class="{on:fmt==='singbox'}" @click="fmt='singbox'">sing-box</span>
        </div>
        <div class="copybox"><code>{{url}}</code><button class="btn sm primary" @click="$copy(url,'已复制')">复制</button></div>
        <div class="divider"></div>
        <div class="between"><span class="muted small">导入后客户端会自动更新流量与到期</span><button class="btn sm danger" @click="reset">重置链接</button></div>
      </div>
      <div class="card" style="display:grid;place-items:center">
        <div class="card-h" style="width:100%"><h3>扫码导入</h3></div>
        <qr :text="url"/>
        <div class="small dim mt">{{ {default:'通用',clash:'Clash',singbox:'sing-box'}[fmt] }} 格式</div>
      </div>
    </div>

    <div class="card mt">
      <div class="card-h"><h3>节点列表 <span v-if="reloading" class="spin"></span></h3><div class="flex" style="gap:8px">
        <button class="btn sm" :disabled="busyAll||pinging||reloading" @click="disableAll">一键禁用全部</button>
        <button class="btn sm" :disabled="busyAll||pinging||reloading||!disabledCount" @click="enableAll">全部启用</button>
        <button class="btn sm" :disabled="pinging||reloading" @click="testSpeed"><span v-if="pinging" class="spin"></span><span v-else>测速</span></button>
      </div></div>
      <div v-if="reloading && !nodes.length" class="empty small"><span class="spin"></span> 正在加载节点…</div>
      <div v-else-if="!nodes.length" class="empty small">订阅暂无节点</div>
      <template v-else>
        <div class="flex wrap mb" style="gap:8px;align-items:center">
          <input class="input" style="max-width:200px" v-model="q" placeholder="搜索节点名 / 服务器">
          <select class="input" style="max-width:130px" v-model="proto">
            <option value="">全部协议</option>
            <option v-for="p in protocols" :key="p" :value="p">{{p}}</option>
          </select>
          <select v-if="groupNames.length" class="input" style="max-width:160px" v-model="group">
            <option value="">全部分组</option>
            <option v-for="g in groupNames" :key="g" :value="g">{{g}}</option>
          </select>
          <span class="small">延迟</span><input class="input" type="number" min="0" style="max-width:72px" v-model="latMin" placeholder="最小"><span class="small">~</span><input class="input" type="number" min="0" style="max-width:72px" v-model="latMax" placeholder="最大"><span class="small dim">ms</span>
          <label class="small flex" style="gap:5px;align-items:center;cursor:pointer"><input type="checkbox" v-model="showDisabled">显示已禁用</label>
          <span class="small dim">{{filteredNodes.length === nodes.length ? ('共 ' + nodes.length + ' 个') : ('筛出 ' + filteredNodes.length + ' / ' + nodes.length + ' 个')}}<span v-if="disabledCount"> · 已禁用 {{disabledCount}}</span></span>
        </div>
        <div class="flex wrap mb" style="gap:8px;align-items:center">
          <span class="small dim">已选 {{selectedKeys.length}}：</span>
          <button class="btn sm" :disabled="busyAll||!selectedKeys.length" @click="bulkSelected(false)">启用所选</button>
          <button class="btn sm danger" :disabled="busyAll||!selectedKeys.length" @click="bulkSelected(true)">禁用所选</button>
          <span class="small dim" style="margin-left:8px">对当前筛选结果：</span>
          <button class="btn sm" :disabled="busyAll||!filteredNodes.length" @click="bulkFiltered(false)">全部启用</button>
          <button class="btn sm danger" :disabled="busyAll||!filteredNodes.length" @click="bulkFiltered(true)">全部禁用</button>
        </div>
        <div class="flex wrap mb" style="gap:8px;align-items:center;padding:8px 10px;background:var(--bg-soft);border-radius:8px">
          <span class="small">条件启用：延迟</span>
          <input class="input" type="number" min="0" style="max-width:80px" v-model="condMin"><span class="small">~</span>
          <input class="input" type="number" min="0" style="max-width:80px" v-model="condMax"><span class="small">ms 的节点启用，其余禁用</span>
          <button class="btn sm primary" :disabled="busyAll||pinging||!tested" @click="applyCond">应用</button>
          <span v-if="!tested" class="small dim">（需先测速）</span>
          <span v-else class="small dim">UDP 节点(tuic/hy2)不受影响</span>
        </div>
        <div v-if="!filteredNodes.length" class="empty small">{{disabledCount && !showDisabled ? '当前节点都已禁用，勾选「显示已禁用」可重新启用' : '没有匹配的节点'}}</div>
        <table v-else class="table"><thead><tr><th style="width:28px"><input type="checkbox" :checked="filteredAllSelected" @change="toggleAllFiltered" title="全选当前筛选"></th><th>节点</th><th>协议</th><th v-if="groupNames.length">分组</th><th>服务器</th><th>延迟</th><th></th></tr></thead>
        <tbody><tr v-for="(n,i) in pagedNodes" :key="n.key||(n.protocol+'|'+n.server+'|'+n.port+'|'+i)" :style="n.disabled?'opacity:.45':''">
          <td><input type="checkbox" :checked="!!sel[n.key]" @change="toggleSel(n)"></td>
          <td><b>{{nodeName(n.name)}}</b></td>
          <td><span class="badge">{{n.protocol}}</span></td>
          <td v-if="groupNames.length" class="small dim" style="white-space:nowrap">{{n.group||'—'}}</td>
          <td class="small dim" style="white-space:nowrap">{{n.server}}:{{n.port}}</td>
          <td style="white-space:nowrap">
            <span v-if="n.udp" class="small dim">客户端测速</span>
            <span v-else-if="n.ok" class="badge" :class="n.latency<150?'green':(n.latency<400?'amber':'red')">{{n.latency}} ms</span>
            <span v-else-if="tested" class="badge red">超时</span>
            <span v-else class="small dim">—</span>
          </td>
          <td style="white-space:nowrap"><button class="btn sm" :class="n.disabled?'':'danger'" @click="toggleNode(n)">{{n.disabled?'启用':'禁用'}}</button></td>
        </tr></tbody></table>
        <div v-if="totalPages>1" class="flex wrap mt" style="gap:10px;align-items:center;justify-content:center">
          <button class="btn sm" :disabled="page<=1" @click="page--">上一页</button>
          <span class="small dim">第 {{Math.min(page,totalPages)}} / {{totalPages}} 页</span>
          <button class="btn sm" :disabled="page>=totalPages" @click="page++">下一页</button>
        </div>
      </template>
      <div class="small dim mt">禁用仅对你自己生效：被禁用的节点不会出现在你导入客户端的订阅里。延迟为「面板服务器 → 节点」的 TCP 探测，仅供参考；UDP 协议(tuic/hy2) 请在客户端测速。</div>
    </div>
  </div>
  <div v-else class="empty" style="padding:56px;text-align:center"><span class="spin"></span> 正在加载订阅…</div>`,
});

/* ============ USER: SHOP ============ */
const PKG_META = { traffic: { t: '流量包', c: 'blue' }, plan: { t: '订阅套餐', c: 'green' }, device: { t: '设备扩容', c: 'amber' } };
app.component('view-shop', {
  data() { return { pkgs: [], busy: 0 }; },
  async mounted() { this.pkgs = await this.$api('/api/user/packages') || []; },
  methods: {
    meta(t) { return PKG_META[t] || { t: t, c: '' }; },
    desc(p) { const x = []; if (p.traffic_bytes) x.push('+' + this.$fmt(p.traffic_bytes) + ' 流量'); if (p.device_add) x.push('+' + p.device_add + ' 设备'); if (p.duration_days) x.push(p.duration_days + ' 天'); return x.join(' · '); },
    async buy(p) {
      if (!confirm('确认用 ' + p.price_points + ' 积分购买「' + p.name + '」？')) return;
      this.busy = p.id;
      try { const r = await this.$api('/api/user/purchase', { method: 'POST', body: { package_id: p.id } }); this.$toast('购买成功，余额 ' + r.points); }
      catch (e) { this.$toast(e.message, true); }
      this.busy = 0;
    },
  },
  template: `
  <div>
    <h1 class="page-title">商城</h1><div class="page-sub">用积分选购流量与套餐</div>
    <div v-if="!pkgs.length" class="empty">暂无上架商品</div>
    <div class="grid cols-3">
      <div class="card" v-for="p in pkgs" :key="p.id">
        <div class="between mb"><span class="badge" :class="meta(p.type).c">{{meta(p.type).t}}</span></div>
        <h3 style="font-size:16px">{{p.name}}</h3>
        <div class="small muted mt" style="min-height:18px">{{p.description||desc(p)}}</div>
        <div class="small dim mt">{{desc(p)}}</div>
        <div class="divider"></div>
        <div class="between">
          <div><span style="font-size:22px;font-weight:720">{{p.price_points}}</span> <span class="small dim">积分</span><div class="small dim">{{$yuan(p.price_points)}}</div></div>
          <button class="btn primary" :disabled="busy===p.id" @click="buy(p)"><span v-if="busy===p.id" class="spin"></span><span v-else>购买</span></button>
        </div>
      </div>
    </div>
  </div>`,
});

/* ============ USER: ORDERS ============ */
app.component('view-orders', {
  data() { return { orders: [] }; },
  async mounted() { this.orders = await this.$api('/api/user/orders') || []; },
  methods: { meta(t) { return PKG_META[t] || { t: t || '其它', c: '' }; } },
  template: `
  <div>
    <h1 class="page-title">我的订单</h1><div class="page-sub">历史购买记录</div>
    <div class="card">
      <div v-if="!orders.length" class="empty">还没有购买记录，去<a style="cursor:pointer" @click="$go('/shop')">商城</a>看看吧</div>
      <table v-else class="table"><thead><tr><th>商品</th><th>类型</th><th>消费积分</th><th>状态</th><th>时间</th></tr></thead>
      <tbody><tr v-for="o in orders" :key="o.id">
        <td><b>{{o.name||('商品#'+o.package_id)}}</b></td>
        <td><span class="badge" :class="meta(o.type).c">{{meta(o.type).t}}</span></td>
        <td>{{o.price_points}}</td>
        <td><span class="badge" :class="o.status==='refunded'?'amber':'green'">{{o.status==='success'?'成功':(o.status==='refunded'?'已退款':o.status)}}</span></td>
        <td class="small dim">{{$date(o.created_at)}}</td>
      </tr></tbody></table>
    </div>
  </div>`,
});

/* ============ USER: POINTS ============ */
const TX_LABEL = { admin_recharge: '管理员充值', purchase: '消费', signup_bonus: '注册赠送', refund: '退款', adjust: '调整', admin_grant: '管理员开通' };
app.component('view-points', {
  data() { return { balance: 0, tx: [] }; },
  async mounted() { const d = await this.$api('/api/user/points'); this.balance = d.balance; this.tx = d.transactions || []; },
  methods: { lab(t) { return TX_LABEL[t] || t; } },
  template: `
  <div>
    <h1 class="page-title">积分</h1><div class="page-sub">积分由管理员充值，可在商城消费</div>
    <div class="card mb stat" style="max-width:260px"><div class="label">当前余额</div><div class="value">{{balance}}</div><div class="sub">{{$yuan(balance)}}</div></div>
    <div class="card">
      <div class="card-h"><h3>积分流水</h3></div>
      <div v-if="!tx.length" class="empty">暂无流水</div>
      <table v-else class="table"><thead><tr><th>类型</th><th>变动</th><th>余额</th><th>备注</th><th>时间</th></tr></thead>
      <tbody><tr v-for="t in tx" :key="t.id"><td>{{lab(t.type)}}</td><td :style="{color:t.amount>=0?'var(--accent-strong)':'var(--danger)',fontWeight:600}">{{t.amount>=0?'+':''}}{{t.amount}}</td><td>{{t.balance_after}}</td><td class="small dim">{{t.note}}</td><td class="small dim">{{$date(t.created_at)}}</td></tr></tbody></table>
    </div>
  </div>`,
});

/* ============ USER: NOTICES ============ */
app.component('view-notices', {
  computed: { notices() { return store.notices || []; } },
  async mounted() { try { await this.$api('/api/user/announcements/read', { method: 'POST' }); } catch (e) {} await window.qz.loadNotices(); },
  template: `
  <div>
    <h1 class="page-title">公告</h1><div class="page-sub">来自管理员的通知</div>
    <div v-if="!notices.length" class="empty">暂无公告</div>
    <div v-for="n in notices" :key="n.id" class="card mb" :style="n.pinned?'border-left:3px solid var(--accent)':''">
      <div class="flex" style="gap:8px"><h3 style="font-size:16px">{{n.title}}</h3><span v-if="n.pinned" class="badge amber">置顶</span></div>
      <div class="muted" style="white-space:pre-wrap;margin-top:8px">{{n.content}}</div>
      <div class="small dim" style="margin-top:8px">{{$date(n.created_at)}}</div>
    </div>
  </div>`,
});

/* admin views are in views-admin.js */
})();
