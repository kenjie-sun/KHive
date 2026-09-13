export type PolicySwitches = {
  network_enabled: boolean
  vowifi_enabled: boolean
  airplane_enabled: boolean
}

export type PolicySwitch = keyof PolicySwitches

export function normalizePolicyICCID(iccid: string): string {
  return iccid.replace(/\s+/g, '').replace(/f$/i, '')
}

export function nextPolicyState(current: PolicySwitches, field: PolicySwitch, enabled: boolean): PolicySwitches {
  const next = {
    network_enabled: current.network_enabled,
    vowifi_enabled: current.vowifi_enabled,
    airplane_enabled: current.airplane_enabled,
    [field]: enabled
  }
  if (enabled && field === 'network_enabled') {
    next.vowifi_enabled = false
    next.airplane_enabled = false
  } else if (enabled && field === 'vowifi_enabled') {
    next.network_enabled = false
  } else if (enabled && field === 'airplane_enabled') {
    next.network_enabled = false
    next.vowifi_enabled = false
  }
  return next
}
