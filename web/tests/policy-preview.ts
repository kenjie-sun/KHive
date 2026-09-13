// Development-only fixture. Axios never reaches a real API in this page.
import { createApp, h, ref } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import '../src/style.css'
import Policy from '../src/components/EsimCardPolicyInline.vue'
import { api } from '../src/stores/auth'
import { nextPolicyState, type PolicySwitch } from '../src/components/cardPolicyState'
const live = ref(false), online = ref(true), fail = ref(false), calls = ref<string[]>([])
let stored = { iccid: '8986000000000000001', network_enabled: false, vowifi_enabled: false, airplane_enabled: false, ip_version: 'v4', apn: '', source: 'user' }
api.defaults.adapter = async config => {
  await new Promise(resolve => setTimeout(resolve, 120))
  const method = config.method || 'get', url = config.url || ''
  calls.value.push(`${method.toUpperCase()} ${url}`)
  if (fail.value) throw new Error('模拟保存失败，原策略保留')
  const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
  if (url === '/cards/8986000000000000001/policy' && method === 'put') stored = { ...stored, ...body }
  else if (url.startsWith('/devices/mock-device/') && method === 'patch') {
    const fields: Record<string, PolicySwitch> = { network: 'network_enabled', vowifi: 'vowifi_enabled', 'flight-mode': 'airplane_enabled' }
    const field = fields[url.split('/').at(-1) || '']
    if (!field) throw new Error('Unexpected fixture request')
    stored = { ...stored, ...nextPolicyState(stored, field, body.enabled) }
  } else if (!(url === '/cards/8986000000000000001/policy' && method === 'get')) throw new Error('Unexpected fixture request')
  return { data: { ...stored }, status: 200, statusText: 'OK', headers: {}, config }
}
createApp({ setup() { return () => h('main', { class: 'max-w-3xl mx-auto p-6 space-y-6' }, [
  h('h1', { class: 'text-2xl font-bold' }, 'KHive 单卡策略'),
  h('p', { class: 'text-gray-500' }, '隔离验证 · 所有卡片与响应均为模拟数据'),
  h('div', { class: 'flex flex-wrap gap-4' }, [
    h('button', { onClick: () => live.value = !live.value }, live.value ? '切换为未激活卡' : '切换为当前卡'),
    h('button', { onClick: () => online.value = !online.value }, online.value ? '模拟离线' : '恢复在线'),
    h('button', { onClick: () => fail.value = !fail.value }, fail.value ? '恢复保存成功' : '模拟保存失败'),
  ]),
  h('section', { class: 'border rounded-xl p-4 space-y-3' }, [h('h2', { class: 'font-semibold' }, '测试 eSIM · 0001'), h(Policy, { deviceId: 'mock-device', iccid: stored.iccid, currentIccid: live.value ? stored.iccid + 'F' : '8986000000000000002', deviceOnline: online.value })]),
  h('pre', { class: 'text-xs overflow-auto bg-gray-50 p-3 rounded' }, calls.value.join('\n'))
]) } }).use(createPinia()).use(ElementPlus).mount('#app')
