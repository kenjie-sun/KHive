import assert from 'node:assert/strict'
import test from 'node:test'
import { applyOptimisticActiveState, normalizeProfileGroups } from '../src/components/deviceEsimOptimistic'
import type { EsimEUICCProfiles } from '../src/types/api'

test('switch succeeds with an empty eUICC partition serialized as null', () => {
  const groups = [
    { eid: 'test', aid_hex: 'SE0', profiles: [
      { iccid: '8986000000000000001', state: 1 },
      { iccid: '8986000000000000002f', state: 0 }
    ] },
    { eid: 'test-empty', aid_hex: 'SE1', profiles: null }
  ] as unknown as EsimEUICCProfiles[]
  const next = applyOptimisticActiveState(groups, '8986000000000000002', 'SE0')
  assert.deepEqual(next[0].profiles.map(p => p.state), [0, 1])
  assert.deepEqual(next[1].profiles, [])
  assert.equal(groups[0].profiles[0].state, 1, 'input snapshot remains unchanged')
})

test('normalizes empty partitions before target search and tolerates an absent list', () => {
  const groups = [{ eid: 'empty', aid_hex: 'SE0', profiles: null }] as unknown as EsimEUICCProfiles[]
  assert.deepEqual(applyOptimisticActiveState(groups, 'unknown', 'SE1')[0].profiles, [])
  assert.deepEqual(normalizeProfileGroups(null), [])
  assert.deepEqual(normalizeProfileGroups(undefined), [])
})
