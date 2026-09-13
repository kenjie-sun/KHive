<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { cardsService } from '../services/cards'
import { devicesService } from '../services/devices'
import type { CardPolicy } from '../types/api'
import { nextPolicyState, normalizePolicyICCID, type PolicySwitch } from './cardPolicyState'

const props = defineProps<{
  deviceId: string
  iccid: string
  currentIccid?: string
  deviceOnline: boolean
}>()
const emit = defineEmits<{ policyChanged: [] }>()
const policy = ref<CardPolicy | null>(null)
const loading = ref(false)
const pending = ref(false)
const error = ref('')
let generation = 0
const normalizedICCID = computed(() => normalizePolicyICCID(props.iccid))
const live = computed(() => props.deviceOnline && normalizedICCID.value === normalizePolicyICCID(props.currentIccid || ''))
const note = computed(() => live.value ? '' : props.deviceOnline
  ? '改动将在此卡激活后生效' : '设备离线，改动保存后将在激活/上线时生效')
const switches: { field: PolicySwitch; label: string }[] = [
  { field: 'network_enabled', label: '网络' },
  { field: 'vowifi_enabled', label: 'VoWiFi' },
  { field: 'airplane_enabled', label: '飞行' }
]

async function load() {
  const request = ++generation
  loading.value = true
  policy.value = null
  error.value = ''
  const result = await cardsService.getPolicy(normalizedICCID.value)
  if (request !== generation) return
  loading.value = false
  if (result.ok) policy.value = result.data
  else error.value = result.error.message || '策略加载失败'
}

async function toggle(field: PolicySwitch, value: string | number | boolean) {
  if (!policy.value || pending.value || loading.value) return
  const request = generation
  const previous = policy.value
  const next = nextPolicyState(previous, field, value === true)
  pending.value = true
  error.value = ''
  try {
    let result
    if (!live.value) {
      result = await cardsService.putPolicy(normalizedICCID.value, next)
    } else if (field === 'network_enabled') {
      result = value ? await devicesService.startNetwork(props.deviceId, {
        ip_version: previous.ip_version, apn: previous.apn
      }) : await devicesService.stopNetwork(props.deviceId)
    } else if (field === 'vowifi_enabled') {
      result = value ? await devicesService.enableVoWiFi(props.deviceId) : await devicesService.disableVoWiFi(props.deviceId)
    } else {
      result = await devicesService.setFlightMode(props.deviceId, value === true)
    }
    if (request !== generation) return
    if (!result.ok) {
      error.value = result.error.message || '策略未生效，请重试'
      return
    }
    policy.value = { ...previous, ...next }
    emit('policyChanged')
  } finally {
    pending.value = false
  }
}

watch(() => [props.deviceId, normalizedICCID.value, props.currentIccid, props.deviceOnline], load, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <div class="basis-full rounded-lg bg-gray-50 dark:bg-white/5 p-3 space-y-3" aria-label="eSIM 卡策略">
    <div v-if="loading" class="text-xs text-gray-500" role="status">正在加载策略...</div>
    <template v-else>
      <div v-if="note" class="text-xs text-orange-600 dark:text-orange-400">{{ note }}</div>
      <div v-if="policy" class="grid grid-cols-1 sm:grid-cols-3 gap-2">
        <div v-for="item in switches" :key="item.field" class="flex items-center justify-between gap-2 rounded-lg bg-white dark:bg-white/5 px-3 py-2">
          <span class="text-sm text-gray-700 dark:text-gray-200">{{ item.label }}</span>
          <el-switch
            :model-value="policy[item.field]"
            :aria-label="item.label"
            :loading="pending"
            :disabled="pending || (item.field === 'network_enabled' && (policy.vowifi_enabled || policy.airplane_enabled)) || (item.field === 'airplane_enabled' && policy.vowifi_enabled)"
            @change="toggle(item.field, $event)"
          />
        </div>
      </div>
      <div v-if="error" class="flex items-center gap-2 text-xs text-red-600 dark:text-red-400" role="alert">
        <span>{{ error }}</span>
        <el-button v-if="!policy" size="small" text @click="load">重试</el-button>
      </div>
    </template>
  </div>
</template>
