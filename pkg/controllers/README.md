# Controllers

Controllers in the `pkg/controllers` package are responsible for reconciling the
APIS and multi-cluster objects.

`tenancy/organizations` - reconciles `workspaces.faros.sh` objects using `tenancy.faros.sh` apiExport
in virtualWorkspace of `tenancy.faros.sh` of workspace `root:faros:controllers`



There are 2 types of controllers:
- `tenancy` - these controllers operate in individual `virtualWorkspaces` (like `tenancy.faros.sh`, etc)
and are responsible for reconciling the objects in tenant workspaces.
