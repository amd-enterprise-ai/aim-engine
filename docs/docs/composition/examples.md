# Composition example manifests

These manifests use the namespaces and prerequisites described in
[Deploy a composition](guide.md). Profile and model references must already
exist and satisfy the installed integration. Apply individual examples, not the
entire directory: they are independent authoring alternatives, and the runtime
configuration example changes namespace/cluster defaults.

| Manifest | Demonstrates |
| --- | --- |
| [01-minimal.yaml](examples/01-minimal.yaml) | Model name, P/D mode, automatic role selection. |
| [02-inline.yaml](examples/02-inline.yaml) | Mode inferred from roles; throughput prefill, latency decode, two decode groups. |
| [03-aggregated.yaml](examples/03-aggregated.yaml) | Exact namespace profile; no P/D transport. |
| [04-cluster-recipe.yaml](examples/04-cluster-recipe.yaml) | Model-agnostic cluster recipe and a model-specific consumer. |
| [05-namespace-recipe.yaml](examples/05-namespace-recipe.yaml) | Namespace recipe with exact binding and consumer capacity. |
| [06-runtime-defaults.yaml](examples/06-runtime-defaults.yaml) | Cluster defaults, transport preset, namespace precision override. |
| [07-default-override.yaml](examples/07-default-override.yaml) | Explicit decode metric wins over the runtime default. |
| [08-default-opt-out.yaml](examples/08-default-opt-out.yaml) | Skip selector defaults without disabling transport requirements. |
| [09-launch-scaling.yaml](examples/09-launch-scaling.yaml) | Role launch/resources, OpenTelemetry metric, and public routing. |
| [10-worker.yaml](examples/10-worker.yaml) | Primary/worker binding and group parallelism. Requires a supported mapping. |
| [11-artifact-selector.yaml](examples/11-artifact-selector.yaml) | Select by model identity without an AIMModel declaration. |
| [12-zero-minimum.yaml](examples/12-zero-minimum.yaml) | Scale both roles from zero using a supported activation integration. |

The contract checker validates these authoring shapes, not the existence of
their external references or hardware compatibility. The
[CRD contract](api.md#admission-and-reconciliation) distinguishes those checks.
