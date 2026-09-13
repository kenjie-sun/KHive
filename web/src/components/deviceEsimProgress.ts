import type { EsimSwitchProgress } from '../types/api'

export function switchProgressNotice(progress: EsimSwitchProgress | null | undefined) {
  if (!progress) return null
  if (!progress.busy) {
    return progress.phase === 'done'
      ? { title: '切卡完成', description: '当前卡的策略已应用，所需网络已就绪。', type: 'success' as const }
      : failureNotice(progress)
  }
  const stages: Record<string, string> = {
    prepare: '正在准备切卡',
    apdu_switching: '正在切换 Profile',
    card_reset_settling: '切卡指令已提交',
    sim_initializing: '正在初始化 SIM',
    transport_recovering: '正在恢复设备连接',
    identity_refresh: '正在确认新卡身份',
    runtime_restore: '正在更新设备状态',
    policy_applying: '正在应用当前卡策略',
    network_restore: '正在恢复蜂窝网络',
    vowifi_restore: '正在恢复 WiFi Calling',
    connectivity_check: '正在确认网络就绪'
  }
  return { title: stages[progress.phase] || '正在结束切卡操作', description: '恢复完成前暂时无法再次切卡。刷新页面可继续查看进度。', type: 'info' as const }
}

export function pendingSwitchSettled(progress: EsimSwitchProgress | null | undefined, token: number | null): boolean {
  return token !== null && !!progress && progress.token >= token && !progress.busy
}

const failureStages: Record<string, string> = {
  prepare: '准备切卡', apdu_switching: '切换 Profile', card_reset_settling: '切换 Profile',
  sim_initializing: 'SIM 初始化', identity_refresh: '新卡身份确认', runtime_restore: '设备状态更新',
  policy_applying: '应用卡策略', network_restore: '蜂窝网络恢复', vowifi_restore: 'WiFi Calling 恢复',
  connectivity_check: '网络就绪确认', transport_recovering: '设备连接恢复'
}
const failureReasons: Record<string, string> = {
  prepare_failed: '未能完成切卡准备，请检查设备连接和 Profile 状态。',
  profile_command_failed: 'Profile 切换指令未能确认成功，请先刷新列表核对当前卡。',
  sim_init_failed: '模组未能完成 SIM 初始化，请检查设备和 SIM 状态。',
  identity_unconfirmed: '尚未确认目标卡的实际身份，请核对当前卡；暂不能确认目标卡策略已生效。',
  policy_apply_failed: '当前卡策略未能应用，请检查该卡的网络、飞行和 WiFi Calling 设置。',
  network_restore_failed: '网络恢复未成功，请检查当前卡的网络设置和连接状态。',
  network_ready_timeout: '等待网络就绪超时，请检查蜂窝连接或 WiFi Calling 注册状态。',
  device_disconnected: '设备已离线或连接已重建，请在重新上线后核对当前卡和网络状态。',
  recovery_timeout: '切卡恢复超过等待时间，请核对当前卡和网络状态。',
  recovery_failed: '恢复未完成，请检查当前卡和网络状态。'
}
function failureNotice(progress: EsimSwitchProgress) {
  const stage = failureStages[progress.failure_phase || '']
  return { title: stage ? `切卡未完成：${stage}` : '切卡未完成', description: failureReasons[progress.failure_code || ''] || failureReasons.recovery_failed!, type: 'warning' as const }
}

// An explicit empty session means the Worker is offline. Undefined remains
// compatible with older servers and temporarily missing client data.
export function switchSessionChanged(previous: string | undefined, current: string | undefined): boolean {
  return previous !== undefined && current !== undefined && previous !== current
}
