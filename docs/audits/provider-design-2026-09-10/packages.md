# Repair packages

All packages implement confirmed findings from README.md on the recorded audit base. Production starts after the initial defect inventory is complete. Workers own only assigned source; the main agent integrates copy synchronization, reviews critical boundaries, and maintains the audit ledger. No push or PR.

| Package | Scope | Findings | Acceptance |
| --- | --- | --- | --- |
| FIX-CORE | Canonical PortalKit confirmation, overflow/layout selector, core styles, row/notification busy wording, decorative icons | SH-01–05, SH-07–10 core, SH-12 | Safe destructive focus, scoped keyboard handling, viewport-bounded menus, consistent confirmation geometry, touch targets (including inputs and associated checkbox hit areas) and preserved accessible action identity; existing and focused behavioral checks |
| FIX-HOST-FLOWS | Host provisioning, enable/onboarding, login/callback, admin/forms, self-host and YAML | HO-01–04, HO-07–14, HO-16 self-host, SH-11 host shell | Honest pending/failure/retry states, associated forms, bounded readable layout and credential lifecycle |
| FIX-HOST-WORKSPACE | Terminal dock, dashboard, MCP and member reads | HO-05–06, HO-15–18, SH-11 MCP | Keyboard equivalents, canonical menus, complete copy feedback, preserved same-scope stale rows without cross-tenant leakage |
| FIX-PROVIDERS | Infrastructure, Databricks, Edges | IN, DB, ED | Schema-valid submit including untouched values; accurate field-error semantics; meaningful accessible status |
| FIX-AGENTS | Agents views and save queue | AG except OAuth | Region-owned truthful save state, correct dirty detection, shared read-state copy and accessible loading/error/empty states |
| FIX-AI | Canonical AgentKit and App Studio | SH-06, SH-09–10 AgentKit, ST findings, SH-11 Studio | Canonical interaction vocabulary and provider-owned behavior retained |
| FIX-AUTH | Dex templates; CLI, hub, Agents, Code HTML completion/error pages | DX-01–02, AU-01, AG-04, CO-01 | Branded bounded status documents with accessible metadata and feedback; preserve auth/CSP/escaping/origin/postMessage semantics |
| VERIFY | Completed package and integration review | All | Focused regressions/builds, design/parity/conformance gates, rendered desktop/mobile and light/dark checks; return production failures to owner |
| DOCS | Design entries and audit resolution | DOC-01, evidence | Verified implementation boundaries, no unsupported runtime claims; all defects and coverage reconciled |
