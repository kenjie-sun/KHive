<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../stores/auth'

type Delivery = { id: string; channel: string; device_id: string; sender: string; state: string; attempts: number; last_error: string; created_at: string }
const rows = ref<Delivery[]>([])
const enabled = ref<string[]>([])
const state = ref('')
const busy = ref(false)
const retrying = ref('')
const labels: Record<string, string> = { pending: '待发送', sending: '发送中', retry: '等待重试', failed: '失败', sent: '已发送' }
let generation = 0
async function load() {
  const current = ++generation
  busy.value = true
  try {
    const { data } = await api.get('/notifications/deliveries', { params: { state: state.value } })
    if (current !== generation) return
    rows.value = data.items || []
    enabled.value = data.enabled_channels || []
  } catch { if (current === generation) ElMessage.error('读取通知投递记录失败') }
  finally { if (current === generation) busy.value = false }
}
async function retry(row: Delivery) {
  retrying.value = row.id
  try { await api.post(`/notifications/deliveries/${encodeURIComponent(row.id)}/retry`); ElMessage.success('已重新排队'); await load() }
  catch { ElMessage.error('重新排队失败，记录状态可能已改变') }
  finally { retrying.value = '' }
}
onMounted(load)
onUnmounted(() => { generation++ })
</script>

<template>
  <section class="rounded-xl border border-gray-200 dark:border-gray-700 p-5 mt-5">
    <div class="flex flex-wrap gap-3 items-center mb-3">
      <h3 class="font-bold mr-auto">短信通知投递记录</h3>
      <el-select v-model="state" placeholder="全部状态" aria-label="通知投递状态" style="width: 145px" @change="load">
        <el-option label="全部状态" value="" />
        <el-option v-for="(label, value) in labels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-button :loading="busy" @click="load">刷新</el-button>
    </div>
    <p class="text-xs text-gray-500 mb-3">显示最近 100 条。失败自动退避重试，最多尝试 10 次；关闭的渠道暂停投递，重新启用后按当前渠道配置继续。网络应答丢失时可能重复通知。</p>
    <el-table class="delivery-desktop" :data="rows" empty-text="暂无投递记录" style="width: 100%">
      <el-table-column prop="channel" label="渠道" width="105" />
      <el-table-column prop="device_id" label="设备" min-width="100" />
      <el-table-column prop="sender" label="发件人" min-width="120" />
      <el-table-column label="状态" width="155"><template #default="{ row }">{{ labels[row.state] || row.state }}{{ row.state !== 'sent' && !enabled.includes(row.channel) ? '（已暂停）' : '' }}</template></el-table-column>
      <el-table-column prop="attempts" label="尝试次数" width="90" />
      <el-table-column prop="last_error" label="失败原因" min-width="190" />
      <el-table-column label="操作" width="100"><template #default="{ row }"><el-button v-if="['failed', 'retry'].includes(row.state)" size="small" :loading="retrying === row.id" :disabled="!enabled.includes(row.channel)" @click="retry(row)">重试</el-button></template></el-table-column>
    </el-table>
    <div class="delivery-mobile">
      <p v-if="!rows.length" class="text-gray-500">暂无投递记录</p>
      <article v-for="row in rows" :key="row.id" class="delivery-card">
        <div class="delivery-card-title"><strong>{{ row.channel }}</strong><span>{{ labels[row.state] || row.state }}{{ row.state !== 'sent' && !enabled.includes(row.channel) ? '（已暂停）' : '' }}</span></div>
        <div>{{ row.device_id }} · {{ row.sender }}</div>
        <div class="text-gray-500">尝试 {{ row.attempts }} 次</div>
        <p v-if="row.last_error">{{ row.last_error }}</p>
        <el-button v-if="['failed', 'retry'].includes(row.state)" size="small" :loading="retrying === row.id" :disabled="!enabled.includes(row.channel)" @click="retry(row)">重试</el-button>
      </article>
    </div>
  </section>
</template>

<style scoped>
.delivery-mobile { display: none; }
.delivery-card { border-top: 1px solid var(--el-border-color-lighter); padding: 12px 0; font-size: 13px; overflow-wrap: anywhere; }
.delivery-card-title { display: flex; justify-content: space-between; gap: 8px; margin-bottom: 4px; }
.delivery-card p { margin: 6px 0; color: var(--el-color-danger); }
@media (max-width: 640px) { .delivery-desktop { display: none; } .delivery-mobile { display: grid; } }
</style>
