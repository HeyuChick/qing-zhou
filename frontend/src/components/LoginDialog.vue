<template>
  <n-modal :show="show" @update:show="$emit('update:show', $event)" preset="card" style="max-width: 400px;" title="">
    <div style="text-align: center; margin-bottom: 16px;">
      <div style="display: inline-flex; align-items: center; gap: 10px; font-weight: 750; font-size: 20px;">
        <div style="width: 34px; height: 34px; border-radius: 10px; background: linear-gradient(135deg, #6f8f76, #5c7c63); display: grid; place-items: center; color: #fff; font-size: 18px;">舟</div>
        轻舟
      </div>
    </div>

    <n-tabs v-model:value="tab" animated>
      <n-tab-pane name="login" tab="登录">
        <n-form ref="loginFormRef" :model="loginForm" :rules="loginRules" label-placement="left">
          <n-form-item label="用户名" path="username">
            <n-input v-model:value="loginForm.username" placeholder="请输入用户名" @keyup.enter="handleLogin" />
          </n-form-item>
          <n-form-item label="密码" path="password">
            <n-input v-model:value="loginForm.password" type="password" show-password-on="click" placeholder="请输入密码" @keyup.enter="handleLogin" />
          </n-form-item>
          <n-button type="primary" block :loading="loading" @click="handleLogin">登录</n-button>
        </n-form>
      </n-tab-pane>

      <n-tab-pane v-if="config.config.registration_open" name="register" tab="注册">
        <n-form ref="regFormRef" :model="regForm" :rules="regRules" label-placement="left">
          <n-form-item label="用户名" path="username">
            <n-input v-model:value="regForm.username" placeholder="请输入用户名" />
          </n-form-item>
          <n-form-item label="密码" path="password">
            <n-input v-model:value="regForm.password" type="password" show-password-on="click" placeholder="请输入密码" />
          </n-form-item>
          <n-form-item v-if="config.config.register_mode === 'code'" label="邀请码" path="code">
            <n-input v-model:value="regForm.code" placeholder="请输入注册码" />
          </n-form-item>
          <n-button type="primary" block :loading="loading" @click="handleRegister">注册</n-button>
        </n-form>
      </n-tab-pane>

      <n-tab-pane name="forgot" tab="找回密码">
        <n-form label-placement="left">
          <n-form-item label="邮箱">
            <n-input v-model:value="forgotEmail" placeholder="请输入注册邮箱" />
          </n-form-item>
          <n-button type="primary" block :loading="loading" @click="handleForgot">发送重置邮件</n-button>
        </n-form>
      </n-tab-pane>
    </n-tabs>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NModal, NTabs, NTabPane, NForm, NFormItem, NInput, NButton, useMessage } from 'naive-ui'
import type { FormRules } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { useConfigStore } from '@/stores/config'
import { apiPost } from '@/api'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()

const router = useRouter()
const auth = useAuthStore()
const config = useConfigStore()
const message = useMessage()
const tab = ref('login')
const loading = ref(false)

const loginForm = reactive({ username: '', password: '' })
const regForm = reactive({ username: '', password: '', code: '' })
const forgotEmail = ref('')

const loginRules: FormRules = {
  username: { required: true, message: '请输入用户名', trigger: 'blur' },
  password: { required: true, message: '请输入密码', trigger: 'blur' },
}
const regRules: FormRules = {
  username: { required: true, message: '请输入用户名', trigger: 'blur' },
  password: { required: true, min: 6, message: '密码至少6位', trigger: 'blur' },
}

async function handleLogin() {
  loading.value = true
  try {
    await auth.login(loginForm.username, loginForm.password)
    message.success('登录成功')
    emit('update:show', false)
    router.push('/dashboard')
  } catch (e: any) {
    message.error(e.message || '登录失败')
  } finally {
    loading.value = false
  }
}

async function handleRegister() {
  loading.value = true
  try {
    await auth.register(regForm.username, regForm.password, regForm.code || undefined)
    message.success('注册成功')
    emit('update:show', false)
    router.push('/dashboard')
  } catch (e: any) {
    message.error(e.message || '注册失败')
  } finally {
    loading.value = false
  }
}

async function handleForgot() {
  if (!forgotEmail.value) { message.warning('请输入邮箱'); return }
  loading.value = true
  try {
    await apiPost('/api/auth/forgot', { email: forgotEmail.value })
    message.success('重置邮件已发送，请查收')
  } catch (e: any) {
    message.error(e.message || '发送失败')
  } finally {
    loading.value = false
  }
}

watch(() => props.show, (v) => {
  if (v) { tab.value = 'login' }
})
</script>
