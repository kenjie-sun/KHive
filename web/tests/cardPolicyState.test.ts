import test from 'node:test'
import assert from 'node:assert/strict'
import { nextPolicyState, normalizePolicyICCID } from '../src/components/cardPolicyState.ts'

test('VoWiFi on/off preserves pre-existing airplane intent', () => {
  const initial = { network_enabled: false, vowifi_enabled: false, airplane_enabled: true }
  const on = nextPolicyState(initial, 'vowifi_enabled', true)
  assert.equal(on.airplane_enabled, true)
  assert.deepEqual(nextPolicyState(on, 'vowifi_enabled', false), initial)
  assert.equal(initial.vowifi_enabled, false)
})
test('network and airplane transitions exclude conflicting services', () => {
  const state = { network_enabled: false, vowifi_enabled: true, airplane_enabled: true }
  assert.deepEqual(nextPolicyState(state, 'network_enabled', true), { network_enabled: true, vowifi_enabled: false, airplane_enabled: false })
  assert.deepEqual(nextPolicyState(state, 'airplane_enabled', true), { network_enabled: false, vowifi_enabled: false, airplane_enabled: true })
})
test('ICCID padding does not turn an active SIM into an inactive policy target', () => {
  assert.equal(normalizePolicyICCID(' 8986 0001f '), '89860001')
})
