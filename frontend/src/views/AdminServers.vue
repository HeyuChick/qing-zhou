<template>
  <div>
    <h2 class="page-title">服务器管理</h2>
    <div class="page-toolbar">
      <span class="spacer" />
      <n-button type="primary" @click="openForm()">添加服务器</n-button>
    </div>
    <n-spin :show="loading">
      <div v-if="servers.length" class="card-grid">
        <div v-for="s in servers" :key="s.id" class="list-card">
          <div class="lc-head">
            <span class="lc-title">{{ s.name || '—' }}</span>
            <n-tag :type="s.enabled ? 'success' : 'default'" size="small" bordered="false">{{ s.enabled ? '启用' : '禁用' }}</n-tag>
          </div>
          <div class="lc-meta">
            <span class="kv">主机 <b>{{ s.host }}</b></span>
            <span class="kv">端口 <b>{{ s.port }}</b></span>
            <span class="kv">探针 <n-tag :type="s.probe_enabled ? 'info' : 'default'" size="tiny" bordered="false">{{ s.probe_enabled ? '开' : '关' }}</n-tag></span>
          </div>
          <div class="lc-foot">
            <n-button size="tiny" @click="handleRebuild(s.id)">重建</n-button>
            <n-button size="tiny" @click="openForm(s)">编辑</n-button>
            <n-button size="tiny" type="error" @click="handleDelete(s.id)">删除</n-button>
          </div>
        </div>
      </div>
      <n-empty v-else-if="!loading" description="暂无服务器" style="padding:40px 0;" />
    </n-spin>

    <n-modal v-model:show="showForm" preset="card" :title="editing?'编辑服务器':'添加服务器'" style="max-width:560px;">
      <n-form label-placement="left" label-width="100">
        <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
        <n-form-item label="主机"><n-input v-model:value="form.host" placeholder="IP 或域名" /></n-form-item>
        <n-form-item label="SSH 端口"><n-input-number v-model:value="form.port" :min="1" :max="65535" style="width:100%;" /></n-form-item>
        <n-form-item label="SSH 用户"><n-input v-model:value="form.ssh_user" placeholder="root" /></n-form-item>
        <n-form-item label="SSH 密码"><n-input v-model:value="form.ssh_password" type="password" show-password-on="click" placeholder="与密钥二选一" /></n-form-item>
        <n-form-item label="SSH 私钥"><n-input v-model:value="form.ssh_key" type="textarea" :rows="3" placeholder="-----BEGIN ... PRIVATE KEY-----" /></n-form-item>
        <n-form-item label="密钥密码"><n-input v-model:value="form.ssh_key_pass" type="password" show-password-on="click" /></n-form-item>
        <n-form-item label="配置路径"><n-input v-model:value="form.config_path" placeholder="/etc/sing-box/config.json" /></n-form-item>
        <n-form-item label="systemd 单元"><n-input v-model:value="form.systemd_unit" placeholder="sing-box" /></n-form-item>
        <n-form-item label="sing-box 路径"><n-input v-model:value="form.sing_box_bin" placeholder="/usr/local/bin/sing-box" /></n-form-item>
        <n-form-item label="v2ray 监听"><n-input v-model:value="form.v2ray_listen" placeholder="127.0.0.1:18080" /></n-form-item>
        <n-form-item label="启用"><n-switch v-model:value="form.enabled" /></n-form-item>
      </n-form>
      <n-space>
        <n-button type="primary" :loading="saving" @click="handleSave">保存</n-button>
        <n-button @click="handleTest" :loading="testing">测试连接</n-button>
      </n-space>
    </n-modal>
  </div>
</template>
<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { NSpin, NButton, NModal, NForm, NFormItem, NInput, NInputNumber, NSwitch, NSpace, NTag, NEmpty, useMessage } from 'naive-ui'
import { apiList, apiPost, apiPut, apiDelete } from '@/api'
const message = useMessage()
const servers = ref<any[]>([])
const loading = ref(false); const saving = ref(false); const testing = ref(false)
const showForm = ref(false); const editing = ref<any>(null)
const mk = () => ({ name:'', host:'', port:22, ssh_user:'root', ssh_password:'', ssh_key:'', ssh_key_pass:'', config_path:'/etc/sing-box/config.json', systemd_unit:'sing-box', sing_box_bin:'/usr/local/bin/sing-box', v2ray_listen:'127.0.0.1:18080', enabled:true })
const form = reactive(mk())
function openForm(s?:any){
  editing.value = s||null
  if(s){ Object.assign(form,{ name:s.name, host:s.host, port:s.port||22, ssh_user:s.ssh_user||'root', ssh_password:'', ssh_key:'', ssh_key_pass:'', config_path:s.config_path||'/etc/sing-box/config.json', systemd_unit:s.systemd_unit||'sing-box', sing_box_bin:s.sing_box_bin||'/usr/local/bin/sing-box', v2ray_listen:s.v2ray_listen||'127.0.0.1:18080', enabled:s.enabled??true }) }
  else { Object.assign(form, mk()) }
  showForm.value = true
}
async function handleSave(){
  saving.value = true
  try{
    const b:any = {...form}
    if(!b.ssh_password) delete b.ssh_password
    if(!b.ssh_key) delete b.ssh_key
    if(!b.ssh_key_pass) delete b.ssh_key_pass
    if(editing.value) await apiPut(`/api/admin/servers/${editing.value.id}`,b)
    else await apiPost('/api/admin/servers',b)
    message.success('保存成功'); showForm.value=false; editing.value=null; await load()
  }catch(e:any){message.error(e.message)}finally{saving.value=false}
}
async function handleTest(){ testing.value=true; try{ const id=editing.value?.id; if(!id){message.warning('请先保存');return} await apiPost(`/api/admin/servers/${id}/test`); message.success('连接成功') }catch(e:any){message.error(e.message)}finally{testing.value=false} }
async function handleRebuild(id:number){ try{await apiPost(`/api/admin/servers/${id}/rebuild`);message.success('重建成功')}catch(e:any){message.error(e.message)} }
async function handleDelete(id:number){ try{await apiDelete(`/api/admin/servers/${id}`);message.success('已删除');await load()}catch(e:any){message.error(e.message)} }
async function load(){loading.value=true;try{servers.value=await apiList('/api/admin/servers')}catch{}finally{loading.value=false}}
onMounted(load)
</script>
