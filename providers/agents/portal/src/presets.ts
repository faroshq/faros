// Agent presets: a starting shape for the kinds of agent people actually
// create, so a capability does not stay undiscovered behind a tool grant and a
// prompt nobody knows to write.
//
// A preset is only defaults. It writes the same fields the Config pane does, and
// everything it sets stays editable afterwards — there is no preset stored on the
// agent and no behavior keyed off one.

import type { AgentCreate } from './types'

export interface AgentPreset {
  id: string
  label: string
  /** One line under the label in the picker. */
  blurb: string
  /** What the user gets, shown as chips so the grant is not a surprise. */
  grants: string[]
  /** Applied over the wizard's own fields on submit. */
  apply: (body: AgentCreate) => AgentCreate
}

// RESEARCH_PROMPT is the fan-out recipe. Model judgment alone is inconsistent —
// the common failure is spawning one worker and joining it before spawning the
// next, which makes the whole thing serial — so the ordering is spelled out.
// Kept in sync with docs/agents-deep-research.md "The research prompt template".
export const RESEARCH_PROMPT = `You are a research agent. You answer questions by gathering evidence and citing it, not from memory alone.

When a question is broad enough to have independent parts, research it in parallel:

1. Decompose it into 3-6 sub-questions that do not depend on each other's answers. If one genuinely depends on another, do that one yourself first.
2. spawn one worker per sub-question. Each task must stand alone - the worker cannot see this conversation, so restate every name, date, version and constraint it needs. Ask for specifics, not a summary.
3. Call join ONCE, after starting all of them. Calling join after each spawn makes the whole thing serial and slow.
4. Read the findings critically. Where two workers disagree, or a claim is load-bearing and thinly sourced, spawn a second short wave to check just that claim.
5. Answer in your own voice, with the sources the workers reported. Say what the evidence does not cover - the gaps are usually the useful part.

For a narrow question, just answer it: searching and fetching directly is faster than a fan-out you don't need.

Do the judgement yourself. A worker reports; you decide.`

export const AGENT_PRESETS: AgentPreset[] = [
  {
    id: 'blank',
    label: 'Blank agent',
    blurb: 'Chat only. Add tools and a persona yourself.',
    grants: [],
    apply: (body) => body,
  },
  {
    id: 'research',
    label: 'Research agent',
    blurb: 'Decomposes a question, works the parts in parallel, answers with sources.',
    grants: ['web search + fetch', 'parallel workers'],
    apply: (body) => ({
      ...body,
      // The wizard's own prompt wins if the user typed one — a preset should
      // never quietly discard what someone wrote.
      systemPrompt: body.systemPrompt?.trim() ? body.systemPrompt : RESEARCH_PROMPT,
      description: body.description || 'Researches questions in parallel and answers with sources.',
      interactiveFamilies: ['core', 'web', 'spawn'],
    }),
  },
]

export function presetByID(id: string): AgentPreset {
  return AGENT_PRESETS.find((p) => p.id === id) || AGENT_PRESETS[0]
}
