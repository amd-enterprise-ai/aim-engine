# AIM Engine

AIM Engine connects reusable model-serving profiles and composition recipes
with deployment intent and the infrastructure available to run them.

## Language

**AIM profile**:
A reusable, pod-local description of model-serving execution, its configuration,
and its requirements.
_Avoid_: Composition profile, deployment graph.

**AIM composition**:
A reusable recipe binding profiles to serving roles and describing their execution
shape and default capacity. A published recipe is not a running service.
_Avoid_: Distributed profile.

**AIM service**:
A concrete intent to expose a model for inference, with instance-specific
deployment choices and observed serving state.

**Serving role**:
A responsibility within a deployment: complete request handling (`main`), prompt
processing (`prefill`), or token generation (`decode`).
_Avoid_: Profile role, which denotes the catalog's base/deployable classification.

**Execution group**:
The complete set of cooperating primary and worker executions that performs one
serving role and is replicated as a whole.
_Avoid_: Worker count, when referring to role replicas.

**Profile binding**:
The association of a profile with a primary or worker execution position, through
an exact reference or selection criteria.

**Profile selector**:
Restrictions on which existing profiles are eligible for a binding.
_Avoid_: Profile overrides, model declaration.

**Selector defaults**:
Administrator-supplied selection criteria for fields left unconstrained by a
recipe and its consumer.
_Avoid_: Mandatory policy, preference ranking, fallback rules.

**Model identity**:
The identity of the actual model artifacts and their revision.
_Avoid_: Served model name.

**Model provenance**:
The publishing AIM model from which a profile originates.
_Avoid_: Model identity.

**Resolved composition**:
The service-specific result of profile selection and application of recipe,
consumer, and runtime settings.

**Serving revision**:
A version of the effective service-specific configuration. A requested revision
can differ from the revision applied to the serving platform.

**Local KV transfer**:
Movement of generation state from prefill to decode through the selected
transport integration. It does not by itself imply same-node placement or GPU
IPC.
_Avoid_: Request routing, intra-role collectives, co-location.

**Interoperability finding**:
A bounded statement about known contradictions, missing construction inputs, or
unverified behavior of a proposed combination.
_Avoid_: Certification, benchmark evidence.
