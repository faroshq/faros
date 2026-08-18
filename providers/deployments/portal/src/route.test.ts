import { parseRoute } from './route.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const malformed = parseRoute('deployments/%E0%A4%A')
assert(malformed.page === 'deployments' && !malformed.name, 'malformed subPath did not fall back to the list route')

const detail = parseRoute('/deployments/production/')
assert(detail.name === 'production', 'valid deployment subPath did not resolve to detail')
