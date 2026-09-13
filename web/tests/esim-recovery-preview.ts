// Development-only fixture: the adapter handles every request in memory.
import { createApp, h, ref } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import '../src/style.css'
import EsimTab from '../src/components/DeviceEsimTab.vue'
import { api } from '../src/stores/auth'
import type { EsimSwitchProgress } from '../src/types/api'
const session = ref('session-1'), online = ref(true), progress = ref<EsimSwitchProgress>(), calls = ref(0)
let token = 0, generation = 1
const target='8986000000000000002', current='8986000000000000001'
api.defaults.adapter = async config => {
 const url=config.url || '', method=config.method || 'get'
 let data: unknown
 if (url==='/devices/mock-device/esim' && method==='get') data={chip_info:{eids:[{eid:'isolated-fixture',aid:'A0000005591010',free_nvram:'512 KB',free_nvram_bytes:524288}]},profiles:[{eid:'isolated-fixture',aid_hex:'A0000005591010',profiles:[{iccid:current,name:'模拟当前卡',service_provider_name:'Fixture',state:1,state_text:'已启用'},{iccid:target,name:'模拟目标卡',service_provider_name:'Fixture',state:0,state_text:'已禁用'}]}]}
 else if (url==='/devices/mock-device/esim/actions/switch' && method==='post') {
  calls.value++;token++
  const submittedToken=token
  progress.value={token,operation:'enable_profile',phase:'sim_initializing',busy:true,started_at:'',updated_at:''}
  await new Promise(resolve=>setTimeout(resolve,1500))
  data={switch_token:submittedToken}
 } else if (url.startsWith('/cards/') && url.endsWith('/policy') && method==='get') data={network_enabled:true,vowifi_enabled:false,airplane_enabled:false,ip_version:'v4',apn:''}
 else throw new Error('Unexpected isolated request: '+method+' '+url)
 return {data,status:200,statusText:'OK',headers:{},config}
}
function fail(phase: string,code: string){progress.value={token,operation:'enable_profile',phase:'degraded',busy:false,failure_phase:phase,failure_code:code,started_at:'',updated_at:''}}
createApp({setup(){return()=>h('main',{class:'max-w-4xl mx-auto p-6 space-y-5'},[
 h('h1',{class:'text-xl font-bold'},'切卡异常恢复 · 隔离验证'),
 h('p',{},'所有卡片、网络和响应均为模拟数据。'),
 h('div',{class:'flex flex-wrap gap-4'},[
  h('button',{onClick:()=>fail('sim_initializing','sim_init_failed')},'模拟 SIM 初始化失败'),
  h('button',{onClick:()=>fail('identity_refresh','identity_unconfirmed')},'模拟身份确认失败'),
  h('button',{onClick:()=>fail('connectivity_check','network_ready_timeout')},'模拟网络超时'),
  h('button',{onClick:()=>{session.value='';online.value=false;progress.value=undefined}},'模拟掉线'),
  h('button',{onClick:()=>{session.value='session-'+(++generation);online.value=true;progress.value=undefined}},'重新上线'),
 ]),
 h(EsimTab,{deviceId:'mock-device',deviceImei:'',isActive:true,deviceOnline:online.value,currentIccid:current,switchProgress:progress.value,esimSession:session.value}),
 h('p',{'data-testid':'request-count'},'切卡请求数：'+calls.value)
])}}).use(createPinia()).use(ElementPlus).mount('#app')
