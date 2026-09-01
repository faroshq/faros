---
name: universal-web-preview
description: Configure or troubleshoot a web preview for a template-less App Studio project in the universal sandbox. Use when an app needs a long-running HTTP process or its preview is not becoming ready; do not use for hosted-template previews or projects without a server.
---

Configure the preview from project and runtime evidence, not from the intended
design alone.

Before declaring a `DevelopmentService`:

- Inspect the application entrypoint, manifest, and existing development
  services to determine the real command, working directory, and port.
- Ensure the server listens on `0.0.0.0` or the equivalent all-interface
  address, not only loopback.
- Treat detected listeners as observations. Do not expose one until the service
  declaration intentionally names it.
- Use private visibility unless the user explicitly requests public access.

Choose the health path conservatively:

- Use `/` when the application root is known to return a fast `2xx` response.
- Use a dedicated path such as `/healthz` only when the application actually
  implements it.
- The health endpoint must be fast, side-effect free, and return `2xx` without
  authentication or redirects.
- Never claim an endpoint exists merely because it was planned or named in the
  `DevelopmentService` declaration.

After creating or updating the service, read its observed status and keep these
signals distinct:

1. The process is running.
2. The declared port is listening.
3. The configured health endpoint is reachable.
4. The route is accepted.
5. Aggregate readiness is true.

Route acceptance and health readiness are independent evidence. An accepted
route can coexist with a failing health check.

Repair the failing layer instead of treating every failure as a routing issue:

- Process stopped: inspect the command and service logs.
- Process running but port not listening: check the declared port and bind
  address.
- Health returns `404`: the configured health path is not implemented; either
  implement it or select an existing safe `2xx` path.
- Health returns `3xx`: use a non-redirecting health endpoint.
- Health succeeds but the route is not accepted: inspect infrastructure
  routing.
- Route is accepted but browser behavior fails: inspect authentication, asset
  loading, browser-console errors, and application behavior.

When the intended service is ready, select it as the primary preview when
appropriate, obtain its preview URL, and inspect the rendered result before
making UI or interaction claims. If readiness does not converge, report the
exact failing signal and do not describe the preview as verified.
