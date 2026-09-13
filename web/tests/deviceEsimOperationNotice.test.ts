import assert from 'node:assert/strict'
import test from 'node:test'
import { describeDeleteResultNotice, describeDownloadTerminalNotice } from '../src/components/deviceEsimOperationNotice'
test('installed or deleted with notification failure stays a warning', () => {
 assert.equal(describeDownloadTerminalNotice({step:'done',msg:'done',pct:100,warning:'通知待重试'}).tone,'warning')
 assert.equal(describeDeleteResultNotice({warning:'通知待重试'}).tone,'warning')
 assert.equal(describeDownloadTerminalNotice({step:'error',msg:'安装失败',pct:-1}).tone,'error')
})
test('space changes are shown only with successful operations and valid measurements', () => {
 assert.match(describeDeleteResultNotice({space_delta:{direction:'released',bytes:1024}}).message,/释放约 1 KB/)
 assert.match(describeDownloadTerminalNotice({step:'done',msg:'',pct:100,space_delta:{direction:'consumed',bytes:2048}}).message,/占用约 2 KB/)
 assert.equal(describeDeleteResultNotice({space_delta:{direction:'released',bytes:0}}).message,'Profile 删除成功')
})
