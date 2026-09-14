<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../stores/auth'
const props = defineProps<{ imsi: string; peer: string; revision: number }>()
type Part = { part_no: number; terminal_state: string; tp_status?: number }
type Delivery = { message_id: string; content: string; state: string; created_at: string; parts: Part[] }
const items = ref<Delivery[]>([])
const error = ref('')
const busy = ref(false)
const labels: Record<string, string> = { pending: '等待终端回执', delivered: '已送达终端', completed: '网络已结束处理', temporary_failure: '临时失败，网络仍在重试', failed: '终端投递失败', unknown: '未知终端状态' }
const submitLabels: Record<string, string> = { pending: '提交中', acked: '网络已接受', partial_ack: '部分分片已接受', failed: '提交失败', unconfirmed: '提交结果未确认' }
let generation = 0
async function load() {
  const current = ++generation
  if (!props.imsi || !props.peer) { items.value = []; return }
  busy.value = true
  try {
    const { data } = await api.get('/sms/deliveries', { params: { imsi: props.imsi, peer: props.peer } })
    if (current === generation) { items.value = data.items || []; error.value = '' }
  } catch { if (current === generation) error.value = '读取回执失败' }
  finally { if (current === generation) busy.value = false }
}
let timer: ReturnType<typeof setInterval> | undefined
watch(() => [props.imsi, props.peer, props.revision], () => { items.value = []; void load() })
onMounted(() => { void load(); timer = setInterval(() => { if (!document.hidden && !busy.value) void load() }, 10000) })
onUnmounted(() => { generation++; clearInterval(timer) })
</script>
<template>
  <details v-if="items.length || error" class="sms-delivery-reports">
    <summary>发送与终端回执 · {{ items.length }} 条</summary>
    <div class="sms-delivery-note">网络接受提交与送达终端分开记录。未收到回执时，无法确认终端是否收到。显示最近 20 条发送记录。</div>
    <el-button size="small" :loading="busy" @click="load">刷新回执</el-button>
    <p v-if="error" role="alert">{{ error }}</p>
    <article v-for="item in items" :key="item.message_id" class="sms-delivery-entry">
      <div class="sms-delivery-text">{{ item.content }}</div>
      <small>{{ new Date(item.created_at).toLocaleString() }} · {{ submitLabels[item.state] || item.state }}</small>
      <div v-for="part in item.parts" :key="part.part_no">分片 {{ part.part_no }}：{{ labels[part.terminal_state] || '未请求终端回执' }}<span v-if="part.tp_status != null">（TP-ST {{ part.tp_status.toString(16).padStart(2, '0') }}）</span></div>
    </article>
  </details>
</template>
<style scoped>
.sms-delivery-reports { margin: 8px 16px; padding: 10px 14px; border: 1px solid var(--el-border-color-light); border-radius: 12px; font-size: 12px; max-height: 260px; overflow: auto; }
summary { cursor: pointer; font-weight: 600; }
.sms-delivery-note { color: var(--el-text-color-secondary); margin: 8px 0; }
.sms-delivery-entry { padding: 8px 0; border-bottom: 1px solid var(--el-border-color-lighter); }
.sms-delivery-text { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; max-width: 100%; }
small { color: var(--el-text-color-secondary); }
</style>
