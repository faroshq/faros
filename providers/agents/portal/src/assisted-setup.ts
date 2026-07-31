// Assisted setup: the pure pieces of "let one of my agents stand up the
// self-hosted search backend for me".
//
// Doing it by hand means creating a websearch Connection here, leaving for the
// Infrastructure provider to provision the searxng template, and coming back to
// paste the resulting URL. The middle step is exactly what an agent can do: the
// hub's aggregate MCP endpoint is dialed for every interactive run, so
// infrastructure__* tools are on the table in chat with no tool grant at all.
//
// The token generator and the prompt composer live here (not inline in the
// view) so both are reviewable and testable on their own.

export type InstanceSize = 'small' | 'medium' | 'large'
export const INSTANCE_SIZES: InstanceSize[] = ['small', 'medium', 'large']

// The DNS-label rule every create form in this portal enforces (K8s object
// names): lowercase alphanumerics and dashes, not starting or ending with one.
export const DNS_LABEL_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

// connectionSecretName mirrors the backend's connectionSecretName(): the
// per-connection Secret the agents provider writes the credential into. The
// searxng instance reads its access token straight out of this Secret, which is
// why the Connection has to exist BEFORE the instance is provisioned.
export function connectionSecretName(connection: string): string {
  return `kedge-agents-conn-${connection}`
}

// TOKEN_ALPHABET is deliberately alphanumeric only. The instance's nginx gate
// compares the token inside a string literal in its generated config, so a
// quote, a backslash or a `$` in the value would either break the config or
// change what it compares against. Length (40) buys back the entropy that a
// wider alphabet would have given: 40 × log2(62) ≈ 238 bits.
const TOKEN_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
const TOKEN_LENGTH = 40

// generateAccessToken returns a CSPRNG token drawn uniformly from
// TOKEN_ALPHABET. Bytes that would bias the modulo (>= the largest multiple of
// 62 under 256) are rejected and redrawn rather than folded in, so every
// character is equally likely.
export function generateAccessToken(length = TOKEN_LENGTH): string {
  const n = TOKEN_ALPHABET.length
  const limit = Math.floor(256 / n) * n // 248 for a 62-char alphabet
  let out = ''
  const buf = new Uint8Array(length * 2)
  while (out.length < length) {
    crypto.getRandomValues(buf)
    for (const b of buf) {
      if (b >= limit) continue
      out += TOKEN_ALPHABET[b % n]
      if (out.length === length) break
    }
  }
  return out
}

export interface SearxngSetupInput {
  // connection is the websearch Connection this portal has just created; its
  // Secret already holds the access token the instance must gate on.
  connection: string
  instance: string
  size: InstanceSize
}

// searxngSetupPrompt composes the single message the agent is handed. It is
// deliberately prescriptive: the agent must discover the template's real input
// schema instead of guessing it, must not invent a token (the Secret exists
// already, and inventing one would silently break search), and must not stop to
// ask questions — nobody is watching the chat to answer them.
export function searxngSetupPrompt({ connection, instance, size }: SearxngSetupInput): string {
  const secret = connectionSecretName(connection)
  return [
    `Please provision a self-hosted SearXNG search backend for me using the infrastructure tools. Work through this end to end without asking me any questions — everything you need is below.`,
    ``,
    `1. Call \`describe_template\` for the \`searxng\` template first and use the exact input schema it returns. Do not guess input names.`,
    `2. Provision it with:`,
    `   - name: \`${instance}\``,
    `   - tokenSecretRef: \`${secret}\``,
    `   - size: \`${size}\``,
    `3. \`${secret}\` is an existing Secret in this workspace that already holds the shared access token. Do NOT generate a token and do NOT try to create or modify that Secret — just reference it by name.`,
    `4. Then poll \`get_instance\` for \`${instance}\` until \`status.url\` is populated. This usually takes a minute or two while the container image is pulled, so wait between checks instead of calling repeatedly back to back.`,
    `5. When \`status.url\` is set, report it to me on its own line, clearly labelled, like:`,
    `   Instance URL: <the status.url value>`,
    `   Then tell me to paste that URL into the Instance URL field of the \`${connection}\` connection (Connections tab) to finish wiring search up.`,
  ].join('\n')
}
