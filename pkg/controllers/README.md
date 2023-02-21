# Controllers

Controllers in the `pkg/controllers` package are responsible for reconciling the
APIS and multi-cluster objects.

There are 2 types of controllers:
- `tenancy` - these controllers operate in individual `virtualWorkspaces` (like `tenancy.faros.sh`, etc)
and are responsible for reconciling the objects in tenant workspaces.
