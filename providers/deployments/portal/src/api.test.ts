import { graphqlPath } from './api.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const escaped = graphqlPath('../evil')
assert(escaped === '/graphql/..%2Fevil', 'tenant path did not encode traversal input as one segment')
assert(!escaped.includes('/graphql/../'), 'tenant path still contains an unescaped traversal separator')
assert(graphqlPath('org/team') === '/graphql/org%2Fteam', 'tenant slash was not encoded inside the path segment')
