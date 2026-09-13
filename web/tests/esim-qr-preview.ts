// Development-only fixture. Every API/fetch request is handled in memory.
import { createApp, h, ref } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import QrcodeVue from 'qrcode.vue'
import 'element-plus/dist/index.css'
import '../src/style.css'
import EsimTab from '../src/components/DeviceEsimTab.vue'
import { api } from '../src/stores/auth'
const session = ref('fixture-1'), calls = ref(0)
const body = ref('')
api.defaults.adapter = async config => {
  if (config.method !== 'get' || !config.url?.endsWith('/esim')) throw new Error('Unexpected isolated API request')
  return { data: { chip_info: { eids: [{ eid: 'fixture-eid', aid: 'A0000005591010', free_nvram: '512 KB', free_nvram_bytes: 524288 }] }, profiles: [] }, status: 200, statusText: 'OK', headers: {}, config }
}
window.fetch = async (url, options) => {
  if (!String(url).endsWith('/devices/qr-fixture/esim/actions/download') || options?.method !== 'POST') throw new Error('Unexpected isolated fetch')
  calls.value++
  body.value = String(options.body)
  return new Response('data: {"step":"done","msg":"模拟下载完成","pct":100}\n\n', { headers: { 'Content-Type': 'text/event-stream' } })
}
createApp({setup(){return()=>h('main',{class:'max-w-4xl mx-auto p-5 space-y-4'},[
  h('h1',{class:'text-xl font-bold'},'二维码照片下载 · 隔离验证'),
  h('p',{},'二维码、设备及下载响应均为模拟数据。'),
  h('div',{class:'flex gap-4'},[
    h('div',{'data-testid':'valid-qr'},[h(QrcodeVue,{value:'LPA:1$rsp.example.com$fixture-token',size:320,margin:4})]),
    h('div',{'data-testid':'unrelated-qr'},[h(QrcodeVue,{value:'https://example.com',size:160,margin:4})])
  ]),
  h('button',{onClick:()=>{session.value += '-new'}},'重建设备会话'),
  h(EsimTab,{deviceId:'qr-fixture',deviceImei:'',deviceOnline:true,isActive:true,esimSession:session.value}),
  h('p',{'data-testid':'download-count'},'下载请求数：'+calls.value),
  h('pre',{'data-testid':'download-body',class:'whitespace-pre-wrap break-all'},body.value)
])}}).use(createPinia()).use(ElementPlus).mount('#app')
