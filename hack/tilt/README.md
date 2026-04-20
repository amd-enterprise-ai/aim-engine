# Tilt Development Workflow

Fast iterative development for the aim-engine operator using Tilt.

## Quick Start

```bash
# Start Tilt (normal mode)
make tilt-up

# Start with Delve debugger attached
make tilt-up-debug

# Tear down (removes controller and all Tilt-managed resources from cluster)
make tilt-down
```

Open http://localhost:10350 to view the Tilt UI.

**Note:** To switch between normal and debug mode, run `make tilt-down` first. The modes use different entrypoints and don't switch cleanly.

## How It Works

1. **Initial build**: Builds a dev image with Go toolchain using BuildKit cache mounts
2. **Push**: Pushes to ttl.sh (ephemeral registry) or loads directly to Kind
3. **Deploy**: Deploys to cluster with PVCs for persistent Go build/mod cache
4. **Iterate**: On file changes, Tilt syncs files into the container and rebuilds in-place
5. **Restart**: The process restarts automatically with the new binary

After the initial build, incremental changes are fast (~10s) because:
- No image rebuild or push needed - just file sync
- Go build cache is preserved in PVCs across restarts
- Only changed packages are recompiled

## Prerequisites

- Tilt installed (`mise install` handles this)
- kubectl configured for a Kind or vcluster context
- Cluster set up with `make kind-create` or `make vcluster-create`

## Context Restrictions

Tilt only allows `kind-*` and `vcluster_*` contexts to prevent accidental deployment to production clusters.

## Debug Mode

In debug mode, Delve listens on port 2345.

```bash
# Start in debug mode
make tilt-up-debug
```

### VS Code Setup

1. Install the Go extension

2. Create `.vscode/launch.json`:
```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Connect to Tilt (Delve)",
            "type": "go",
            "request": "attach",
            "mode": "remote",
            "port": 2345,
            "host": "127.0.0.1",
            "showLog": true,
            "substitutePath": [
                {
                    "from": "${workspaceFolder}",
                    "to": "/workspace"
                }
            ]
        }
    ]
}
```

3. If VS Code complains about missing `dlv`, install it and create `.vscode/settings.json`:
```json
{
    "go.alternateTools": {
        "dlv": "/path/to/your/go/bin/dlv"
    }
}
```

4. Press F5 to connect. Set breakpoints by clicking in the gutter next to line numbers.

### GoLand Setup

Create a "Go Remote" run configuration with host `localhost` and port `2345`.

### CLI Debugging

```bash
dlv connect localhost:2345
```

### Debugger Examples

```bash
# Set a breakpoint by file:line
(dlv) break internal/aimservice/reconcile.go:50

# Set a breakpoint by function name
(dlv) break github.com/amd-enterprise-ai/aim-engine/internal/controller.(*AIMServiceReconciler).Reconcile

# List all breakpoints
(dlv) breakpoints

# List all goroutines (shows controllers, informers, etc.)
(dlv) goroutines

# Switch to a specific goroutine
(dlv) goroutine 80

# Show stack trace of current goroutine
(dlv) stack

# Continue execution until next breakpoint
(dlv) continue

# When stopped at a breakpoint, print a variable
(dlv) print req.NamespacedName

# Print local variables
(dlv) locals

# Step to next line
(dlv) next

# Step into function
(dlv) step

# Exit debugger
(dlv) exit
```

**Note:** When the debugger pauses execution (hitting a breakpoint), the health probe stops responding and the container appears un-ready. This is expected - the entire program is paused. Execution resumes when you `continue`.

## Build Cache

Two PVCs are created in `aim-system` namespace:
- `aim-engine-go-build-cache` (5Gi) - Compiled package cache
- `aim-engine-go-mod-cache` (5Gi) - Downloaded module cache

These persist across pod restarts. The first build populates the cache; subsequent builds are incremental.

The PVCs use `ReadWriteOnce` access mode. To prevent pod restart deadlocks (new pod can't mount the PVC while the old pod still holds it), the deployment uses `strategy.type: Recreate` — Kubernetes terminates the old pod before creating the new one.

## Manual Resources

In the Tilt UI, you can manually trigger:
- **unit-tests**: Run `go test` (excludes e2e)
- **lint**: Run golangci-lint
- **test-chainsaw**: Run all chainsaw e2e tests
- **test-chainsaw-feature**: Run focused tests from `.local/{branch}/feature.yaml`

## Focused Testing

During development, run only the tests relevant to your feature:

1. Create `.local/{branch}/feature.yaml`:
```yaml
description: "Working on frozen model support"

tests:
  - tests/e2e/aimservice/frozen
  - tests/e2e/aimmodel/auto-templates
```

2. Run via Make or Tilt:
```bash
make test-chainsaw-feature
```

Reports are saved to `.local/{branch}/test-reports/{timestamp}-{commit}.json`

## Port Forwards

Tilt automatically forwards:
- `8081` - Health probe endpoint
- `8080` - Metrics endpoint
- `2345` - Delve debugger (debug mode only)
