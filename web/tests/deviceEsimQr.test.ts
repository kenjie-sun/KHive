import assert from 'node:assert/strict'
import test from 'node:test'
import { parseEsimActivationCode, validateQrPhoto } from '../src/components/deviceEsimQr'

test('parses activation fields and confirmation flag without treating metadata as a confirmation code', () => {
  assert.deepEqual(parseEsimActivationCode(' LPA:1$rsp.example.com$fixture-token$1.2.3$1\n'), {
    smdp: 'rsp.example.com', matchingId: 'fixture-token', confirmationRequired: true
  })
  assert.equal(parseEsimActivationCode('LPA:1$rsp.example.com$').matchingId, '')
})

test('rejects unrelated QR codes and malformed activation endpoints', () => {
  for (const text of ['https://example.com', 'LPA:2$rsp.example.com$token', 'LPA:1$$token',
    'LPA:1$rsp.example.com/path$token', 'LPA:1$user@example.com$token', 'LPA:1$rsp.example.com$two tokens']) {
    assert.throws(() => parseEsimActivationCode(text))
  }
})

test('rejects oversized, empty and unsupported images before decoding', () => {
  assert.doesNotThrow(() => validateQrPhoto({ type: 'image/png', size: 1024 }))
  assert.throws(() => validateQrPhoto({ type: 'image/jpeg', size: 11 * 1024 * 1024 }))
  assert.throws(() => validateQrPhoto({ type: 'image/png', size: 0 }))
  assert.throws(() => validateQrPhoto({ type: 'image/heic', size: 1024 }))
})
