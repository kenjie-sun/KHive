import { cpSync, existsSync, rmSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const source = fileURLToPath(new URL('../web/dist/', import.meta.url))
const target = fileURLToPath(new URL('../internal/web/dist/', import.meta.url))
if (!existsSync(`${source}/index.html`)) throw new Error('Build the KHive frontend first')
rmSync(target, { recursive: true, force: true })
cpSync(source, target, { recursive: true })
