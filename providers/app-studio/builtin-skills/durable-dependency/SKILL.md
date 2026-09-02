---
name: durable-dependency
description: Provision and connect a database, cache, queue, or other durable Infrastructure dependency to an App Studio development service. Use when an app must begin depending on a new external service; do not use for ordinary source-only changes or a dependency that is already connected and Ready.
---

Establish the dependency before making the application require it.

Before editing application files or changing a `DevelopmentService`:

1. Use `list_dependency_templates` and `list_project_dependencies`.
2. Choose a provider interface and a compatible target interface for the
   intended development service.
3. If the target interface is missing or incompatible, stop without changing
   files or runtime configuration. Report the exact blocker.
4. Use `upsert_project_dependency` to provision and connect the dependency.
   This is the complete, atomic project-dependency flow: do not call
   `infrastructure__provision`, `infrastructure__update_instance`, or another
   Infrastructure mutation before or after it. Pass the catalog Template name
   in `template`, a project-local connection name in `dependency`, and all
   required public provider inputs in `values`. Copy `template` exactly from
   `list_dependency_templates.templates[].name`; do not substitute the
   dependency name or interface type. For the PostgreSQL catalog entry named
   `database`, pass `template: "database"`, not `postgres` or `postgresql`.
   Omit `values.name` unless the user explicitly chose an infrastructure name;
   App Studio supplies a project-scoped name that avoids collisions with
   retained resources from other Projects.
5. Treat the dependency `environment` as the Project environment only. Do not
   copy it into `values.farosMode`; a standalone dependency can serve a
   development app while using its Template's normal production mode.
6. Let the normal confirmation flow obtain the user's approval. Never inject
   credentials or connection environment variables manually.
7. Wait until `list_project_dependencies` reports the dependency Ready.

Only after the dependency is Ready should the application be changed to require
the mapped environment variable or connection data. Then resolve package
dependencies, run focused build and test checks, and verify the affected
development service is healthy.

Do not claim the dependency is active merely because application files changed
or the Infrastructure Template exists. If provisioning cannot complete, leave
the previously working application intact and report what is still required.
