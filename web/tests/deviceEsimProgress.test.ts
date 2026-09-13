import assert from 'node:assert/strict'
import test from 'node:test'
import { switchProgressNotice, pendingSwitchSettled, switchSessionChanged } from '../src/components/deviceEsimProgress'
import type { EsimSwitchProgress } from '../src/types/api'
const state: EsimSwitchProgress = { token: 7, operation: 'enable_profile', phase: 'connectivity_check', busy: true, started_at: '', updated_at: '' }
test('accepted command and recovered identity do not display completion', () => {
  for (const phase of ['card_reset_settling', 'identity_refresh', 'connectivity_check', 'done']) {
    const progress = { ...state, phase }
    assert.equal(switchProgressNotice(progress)?.type, 'info')
    assert.equal(pendingSwitchSettled(progress, 7), false)
  }
})
test('server snapshot restores progress and distinguishes success from failure', () => {
  assert.equal(switchProgressNotice(JSON.parse(JSON.stringify(state)))?.title, '正在确认网络就绪')
  assert.equal(switchProgressNotice({ ...state, busy: false, phase: 'done' })?.type, 'success')
  assert.equal(switchProgressNotice({ ...state, busy: false, phase: 'degraded' })?.type, 'warning')
  assert.equal(pendingSwitchSettled({ ...state, busy: false }, 7), true)
  assert.equal(pendingSwitchSettled({ ...state, busy: false, token: 6 }, 7), false)
})

test('failure notice identifies the stage and reason without exposing arbitrary error text', () => {
 for (const [phase,code,label] of [['sim_initializing','sim_init_failed','SIM 初始化'],['identity_refresh','identity_unconfirmed','新卡身份确认'],['connectivity_check','network_ready_timeout','网络就绪确认']]) {
  const notice=switchProgressNotice({...state,busy:false,phase:'degraded',failure_phase:phase,failure_code:code})
  assert.ok(notice?.title.includes(label!))
  assert.equal(notice?.type,'warning')
 }
 const notice=switchProgressNotice({...state,busy:false,phase:'failed',failure_code:'private payload'})
 assert.ok(!notice?.description.includes('private payload'))
})
test('replacement and offline sessions invalidate old pending requests', () => {
 assert.equal(switchSessionChanged('old','new'),true)
 assert.equal(switchSessionChanged('old',''),true)
 assert.equal(switchSessionChanged('old','old'),false)
 assert.equal(switchSessionChanged(undefined,'new'),false)
 assert.equal(switchSessionChanged('old',undefined),false)
})
