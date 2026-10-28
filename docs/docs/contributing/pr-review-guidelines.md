# Pull Request Review Guidelines

This document is the canonical checklist for **reviewers** and **authors** evaluating pull requests in AIM Engine. It complements the author-facing conventions in [Coding Guidelines](coding-guidelines.md) with a deeper, production-oriented review lens.

Author conventions (naming, comments, docs/samples, phase discipline) live in Coding Guidelines and should be enforced here. Review-only concerns (scope, cross-layer completeness, upgrade safety, alternative designs) live only here.

Agents helping with authorship or review should follow the condensed standards section in [`CLAUDE.md`](../../../CLAUDE.md) and the full docs linked from there.

The goal of review is not to gatekeep style preferences. It is to ensure merged code is **correct**, **maintainable**, **observable in production**, and **safe to operate** for cluster administrators and downstream users.

---

## Review principles

### Optimize for production outcomes

Ask: *What happens in a real cluster when this ships?* A change can pass unit tests and still be unsafe if it changes defaults silently, breaks upgrade paths, creates object churn, or leaves operators without documentation.

### Consider simpler alternatives — without redesigning the PR

Reviewers **should** look for a meaningfully simpler or more direct solution the author may have missed. That is part of catching maintainability and operability debt early.

Keep it productive:

| Do | Don't |
| -- | ----- |
| Raise a concrete alternative when it removes a layer, shrinks the API surface, reuses an existing helper, or avoids a production hazard | Redesign the PR for taste ("I'd have structured the packages differently") |
| Frame as a **Question** or **Important** with a short sketch of the alternative and why it is better | Block solely because another approach exists |
| Accept the author's approach when they give a reasoned trade-off (time, migration cost, consistency with nearby code) | Relitigate settled ADRs without new evidence |
| Escalate to **Blocker** only when the chosen design is incorrect, unsafe, or creates clear future pain that a small change would avoid | Turn every review into an architecture committee |

Missed simpler approaches are important; open-ended redesign is counter-productive. Prefer "could `FindReadyShared` return a typed stale error instead of failing Ready?" over "have you considered a different caching model?"

### Be specific and actionable

Every blocking comment should state:

1. **What** is wrong or missing.
2. **Why** it matters (correctness, operability, consistency, future maintenance).
3. **What** would resolve it (concrete suggestion, not "please reconsider").

Prefer questions when intent is unclear; prefer requests when the fix is known.

### Separate severity

Use explicit severity so authors can prioritize:


| Label | Meaning |
| ----- | ------- |
| **Blocker** | Must fix before merge (correctness, security, production risk, missing required tests/docs). |
| **Important** | Should fix before merge unless author provides a tracked follow-up with reviewer agreement. |
| **Nit** | Optional polish; author may defer with a brief rationale. |
| **Question** | Clarification needed; not necessarily a change request. |

### Review the whole change, not just the diff

Read the PR description, linked issues/ADRs, and enough surrounding code to judge whether the change is **complete** and **uniform** across layers (API → controller → Helm → docs → tests → CI).

---

## Before you start reviewing

### Author prerequisites (verify first)

- [ ] PR description explains **why**, not only **what**.
- [ ] Linked issue, ADR, or design note when behavior is non-obvious.
- [ ] Scope is focused; unrelated refactors are split out or justified.
- [ ] CI is green (lint, unit tests, relevant workflow legs).
- [ ] Branch is reasonably current with the target branch (rebase/merge if far behind).
- [ ] No secrets, tokens, kubeconfigs, or environment-specific values in the diff.
- [ ] Generated artifacts regenerated where applicable (`make generate`, `make manifests`, `make generate-helm-docs`).

If CI is red or the branch is stale, note it immediately. Do not spend a full architectural review on code that cannot merge yet.

### Reviewer setup

1. Check out the branch locally when the change touches reconciliation, Helm wiring, or e2e behavior.
2. Read the PR description and identify the **claimed behavior change**.
3. Skim the file list for **expected touch points** (see [Cross-layer completeness](#cross-layer-completeness) below).
4. Run targeted tests if CI logs are insufficient or the change is high risk.

---

## Step 1: Scope and intent

### Does the PR match its stated purpose?

- [ ] Every bullet in the description is reflected in the diff.
- [ ] Nothing important is **missing** from the description (surprise behavior changes are called out).
- [ ] The diff does not include unrelated drive-by changes (formatting sweeps, unrelated renames, accidental local edits).
- [ ] Commit history is readable (squash/fixup before merge if needed).

### Is the scope appropriately sized?

Large PRs are harder to review and more likely to hide production issues. Flag when a PR mixes:

- Feature + refactor + docs migration + CI overhaul.
- Multiple independent user-visible behaviors.
- v1alpha1 and v1alpha2 changes without a clear boundary.

Recommend splitting when review time exceeds what a single reviewer can validate with confidence.

### Red flags in PR descriptions

- "Drive-by fixes" without explanation.
- "Tests pass locally" with no CI link or missing workflow legs.
- Behavior changes described as "internal only" but touching defaults, Helm values, or CRD fields.
- Missing upgrade/migration notes for admin-visible changes.

---

## Step 2: Cross-layer completeness

AIM Engine changes often need to propagate through several layers. A common review failure mode is updating one layer while leaving stale copies elsewhere.

Use this matrix when the change touches a given area:


| Change type | Expected touch points |
| ----------- | --------------------- |
| API field / enum / default | `api/*/…_types.go` markers → `make generate` + `make manifests` → CRD YAML → docs reference (`docs/docs/reference/api/`) → unit tests → e2e if user-visible |
| Helm value / operator flag | `config/helm/values.yaml` → `hack/patch-helm-chart.sh` or templates → `docs/docs/reference/helm-values.md` (generated) → `cmd/main.go` flag/default → e2e install path if default lane changes |
| Reconcile behavior | domain package (`internal/v1alpha*/…`) → controller wiring → conditions/status reasons → unit tests on pure plan/compose logic → e2e when crossing controllers |
| New CRD or RBAC rule | API types → `config/rbac/role.yaml` (generated) → chart templates → docs |
| User-facing workflow | guide under `docs/docs/guides/` or `docs/docs/admin/` → samples under `config/samples/` → e2e fixture if feasible |
| Deprecation / removal | ADR or migration note → `docs/docs/admin/upgrading.md` → backward-compat tests → clear condition reasons for users still on old shape |

### Omitted changes (common gaps)

Explicitly look for **missing** updates:

- [ ] **Compiled default** vs **Helm default** vs **flag default** — all three should agree, or the divergence must be documented (e.g. empty Helm value intentionally falls back to compiled default).
- [ ] **CI matrix legs** — if default operator mode changes, the default e2e lane and any mode-gated tests must align (see [Testing](testing.md)).
- [ ] **CHANGELOG** — user-visible or operator-visible changes belong under `[Unreleased]` before release.
- [ ] **Upgrade guide** — default flips, new required fields, new CRD prerequisites, or behavior changes on `helm upgrade` without `--set`.
- [ ] **Generated docs** — hand-editing `helm-values.md` or CRD reference without updating the generator source.
- [ ] **Sample manifests** — new fields or recommended patterns reflected in `config/samples/`.
- [ ] **Pre-commit / Makefile targets** — new generate steps wired if a new artifact type is introduced.

### Lack of uniformity

Flag when the same concept is named, defaulted, or documented differently across layers:


| Symptom | Example | Review action |
| ------- | ------- | ------------- |
| Terminology drift | "projection mode" vs "runtime mode" vs "eager mode" | Pick canonical term from ADR/docs; request alignment |
| Overloaded bare terms | "runtime" / "profile" / "model" used for multiple concepts without qualification | Require disambiguation per [Coding Guidelines](coding-guidelines.md#disambiguate-overloaded-domain-terms) |
| Default mismatch | Go constant `Both`, Helm value `Exhaustive`, docs say Exhaustive | Block until unified or explicitly documented |
| Duplicate logic | Same validation in webhook, reconcile, and Helm template | Prefer single source of truth (CEL/API markers or shared Go helper) |
| Parallel test stories | Unit test asserts X; e2e asserts Y; docs describe Z | Require one authoritative behavior statement |
| Stale comments | File header says "Exhaustive is default" after change to Both | Request comment/doc sync |

---

## Step 3: Code and architecture

Follow [Coding Guidelines](coding-guidelines.md) and [Controller Patterns](controller-patterns.md). Reviewers should enforce phase discipline, naming clarity, and comment quality.

### Design fitness (including missed alternatives)

- [ ] The approach matches existing controller / domain patterns unless the PR justifies a deliberate departure.
- [ ] No new abstraction, interface, or config knob without a second real use case (see Coding Guidelines "Keep it simple").
- [ ] If a simpler local alternative is obvious (reuse helper, typed error, smaller API), raise it — see [Consider simpler alternatives](#consider-simpler-alternatives--without-redesigning-the-pr).

### Controller and domain logic

- [ ] **Fetch → Compose → Plan → Apply → Status** boundaries respected; no `client.Get` inside `PlanResources`.
- [ ] Idempotent reconciliation: second reconcile does not duplicate objects or flap labels/conditions.
- [ ] Owner references and GC behavior correct for created children.
- [ ] Errors wrapped with context; permanent user errors do not infinite-requeue.
- [ ] Conditions use stable `Reason` values and actionable `Message` text.
- [ ] `Ready` reflects actual usability, not merely "reconcile finished".

### Naming and comments

- [ ] Overloaded domain terms are qualified (`KServe ServingRuntime` vs `AIMRuntimeConfig`, etc.).
- [ ] Comments explain current behavior and non-obvious implications — not debugging history or discarded approaches ([Coding Guidelines](coding-guidelines.md#comments)).

### API design

- [ ] Validation expressed via kubebuilder markers / CEL where possible.
- [ ] Defaults are explicit and least-surprising; empty string handling tested.
- [ ] Enum changes reject unknown values at startup or admission, not silently.
- [ ] Backward compatibility preserved within the supported version window (v1alpha1 ↔ v1alpha2 coexistence rules in [Upgrading](../admin/upgrading.md)).

### Configuration and flags

- [ ] New flags documented in Helm values and/or operator help text.
- [ ] Invalid configuration fails loudly at startup (see `ParseRuntimeProjectionMode` pattern).
- [ ] Feature flags and defaults safe for public installs (no cluster-specific assumptions).

### Performance and operability

- [ ] No unbounded lists without field selectors/indexers when scale matters.
- [ ] Requeue intervals reasonable; no hot loops on permanent failures.
- [ ] Log levels appropriate (debug for verbose traces, info for state transitions, error for failures).
- [ ] Object churn acceptable? New defaults that create additional CRs (e.g. extra ServingRuntimes) should be called out for ops impact.

---

## Step 4: Test coverage

Tests are part of the merge contract. Review coverage by **risk**, not line count.

### Unit tests

- [ ] New behavior has tests in the same package as the logic (prefer pure `PlanResources` / matcher tests).
- [ ] Table-driven cases cover happy path, boundary, and failure modes.
- [ ] Tests assert **outputs**, not merely "no error".
- [ ] Bug fixes include a regression test that would have failed before the fix.
- [ ] Realistic fixtures (labels, condition shapes, resource names) per [Coding Guidelines](coding-guidelines.md).

### Integration / envtest

- [ ] Controller wiring tested when watches, indexers, or scheme registration change.
- [ ] Status decoration and condition transitions verified when user-visible.

### E2e (Chainsaw)

- [ ] User-visible flows have e2e coverage when feasible (prefer full-chain → `Ready` over frozen status).
- [ ] Hardware-specific tests use correct `requires` labels (`gpu-amd`, `gpu-nvidia`, `longhorn`, `reduced-mode`, etc.).
- [ ] Default CI lane exercises the **default operator configuration** after the change.
- [ ] Mode-gated tests documented if certain behaviors are intentionally CI-dark without a dedicated leg.

### Coverage gaps to challenge

Ask the author explicitly when any of these apply:

| Gap | Question for author |
| --- | ------------------- |
| Default change | Is there a test for empty/unset config resolving to the new default? |
| Removed e2e test | What still covers the deleted scenario (unit only)? Is that sufficient? |
| Exhaustive-only property | If e2e no longer asserts absence/coexistence, do unit tests pin the contract? |
| Upgrade path | Is there an e2e or integration test for existing resources after upgrade? |
| Cross-version | Does v1alpha1 behavior remain unchanged when v1alpha2 changes? |

There is no fixed coverage percentage, but **behavior changes without tests** require strong justification.

---

## Step 5: Documentation

- [ ] User-facing behavior documented in the right tier: guide, admin, or reference — not only in code comments.
- [ ] ADR updated or referenced when architectural decisions change.
- [ ] Generated reference docs updated via generators, not hand-edited copies.
- [ ] Samples and doc examples copy-paste cleanly (valid YAML, correct apiVersion).
- [ ] Migration window / upgrading docs updated when dispatch rules, annotations, or defaults change.
- [ ] **Docs and samples stand alone** — no references to internal registries, proprietary label namespaces, or closed-source tooling an external contributor cannot resolve ([Coding Guidelines](coding-guidelines.md#docs-and-samples-must-stand-alone)). Internal-only install notes, if any, are isolated and labeled.

### Documentation quality bar

- Explains **why** an operator would choose a setting, not only what it does.
- Callouts for breaking or behavior-changing defaults.
- Links to canonical sections (avoid duplicating dispatch tables that live in [Upgrading](../admin/upgrading.md)).
- Overloaded terms are qualified the same way as in code.

---

## Step 6: Production readiness

Before approving, explicitly evaluate whether this PR is safe to ship to production clusters.

### Upgrade and rollback

- [ ] **Helm upgrade path** documented when chart defaults or values change.
- [ ] **`--reuse-values` behavior** considered: will upgrading without explicit `--set` change runtime behavior?
- [ ] **Rollback** story understood (operator rollback does not revert CRDs — see [Upgrading](../admin/upgrading.md)).
- [ ] **Additive vs destructive** changes identified; destructive changes require release notes and migration steps.

### Default and configuration changes

Treat default changes as **user-visible** even when labeled internal:

- Compiled-in default constant
- Helm `values.yaml` default
- CI install flags for the default e2e lane

For each, ask:

1. What happens to **existing** installs on upgrade?
2. Can admins **pin** the old behavior explicitly?
3. Is that pin documented?

### Operational impact

- [ ] Additional Kubernetes objects created? (CR count, watch load, RBAC scope)
- [ ] Changes to reconciliation frequency or API server load?
- [ ] New external dependencies (network, credentials, GPU nodes, storage classes)?
- [ ] Failure modes visible to admins via conditions/events?

### Security and multi-tenancy

- [ ] No auth bypass on routes, gateways, or webhooks.
- [ ] RBAC changes minimal and justified.
- [ ] No secrets logged or printed in test output.
- [ ] Cross-namespace references validated where applicable.

### Release hygiene

- [ ] CHANGELOG entry for operator-visible changes (can be added at release-prep if policy allows, but flag if missing near release).
- [ ] Version compatibility noted when minimum Kubernetes or dependency versions shift.

---

## Step 7: CI and workflow alignment

- [ ] Required workflows pass (lint, unit tests, docs build if touched).
- [ ] E2e matrix covers new configuration dimensions (e.g. projection mode, GPU vendor legs).
- [ ] Conditional workflow steps (`if: matrix.mode == …`) updated when defaults move.
- [ ] Fork PR secrets gating unchanged or intentionally updated.

When CI cannot run a leg locally, rely on logs but note **unverified legs** in the review summary.

---

## Step 8: Final review summary template

Post a structured summary so authors and future readers know the merge bar:


```markdown
## Review summary

**Verdict:** Approve / Request changes / Comment only

**Production readiness:** Ready / Ready with follow-ups / Not ready

### What looks good
- …

### Blockers
- …

### Important (non-blocking if agreed)
- …

### Alternative approaches considered
- … (or "none — approach fits existing patterns")

### Test coverage notes
- …

### Docs / ops follow-ups
- …

### Uniformity / omitted changes
- …
```

---

## Decision guide


| Situation | Typical action |
| --------- | -------------- |
| Correctness bug or data loss risk | **Block** |
| Missing test for new behavior | **Block** (or agree on immediate follow-up issue for tiny hotfix exceptions) |
| Default change without upgrade note | **Block** or **Important** depending on impact |
| Docs/samples leak internal-only registries or tooling | **Block** or **Important** |
| Ambiguous overloaded term in user-facing docs or API comments | **Important** |
| Comment is a process diary / discarded-alternative narrative | **Important** (or **Nit** if tiny) |
| Clearer local alternative author may have missed | **Question** or **Important**; **Block** only if current design is unsafe/incorrect |
| Open-ended redesign preference | Abstain or **Nit** |
| Stale doc in unrelated file | **Nit** or **Important** if misleading for the changed feature |
| Style preference outside guidelines | **Nit** or abstain |
| Large PR with good code but high review risk | Request split |
| Green CI, complete layers, clear docs | **Approve** |

### When to approve with follow-ups

Approve only when follow-ups are **non-production-critical** and tracked (issue linked, milestone set). Do not approve with follow-ups for: missing regression tests on defaults, undocumented upgrade behavior, or unresolved cross-layer inconsistency.

---

## Author response expectations

Authors should:

1. Reply to every **Blocker** and **Important** comment (fixed, or explained with reviewer agreement).
2. Push fix commits or rebase cleanly; avoid force-push that drops review context without notice.
3. Update the PR description if scope or behavior changed during review.
4. Re-request review only after CI is green and requested changes addressed.
5. When an alternative is raised: either adopt it, or briefly record why the current approach is preferred (consistency, migration cost, ADR).

Reviewers should re-review **delta** after updates, but re-check blockers and production-readiness items in full.

---

## Project-specific hot spots

These areas have historically high coupling and deserve extra scrutiny:

### Runtime projection (v1alpha2)

- Projection mode (`Exhaustive` / `Reduced` / `Both`) must stay consistent across: Go default, Helm value, operator flag, CI matrix, e2e smokes, and [ADR 0008](../../adr/0008-runtime-projection-from-profiles.md).
- Qualify "runtime" carefully: projected `ServingRuntime` objects vs `AIMRuntimeConfig` vs reconcile behavior.
- `autoSelect` semantics: all projected runtimes keep `autoSelect: false` — flag doc/table drift.
- Lazy vs eager projection: mode-governed vs always-on paths must not be conflated in docs or tests.

### v1alpha1 ↔ v1alpha2 migration window

- AIMService dispatch is by **spec shape**, not apiVersion alone — see [Upgrading](../admin/upgrading.md).
- New v1alpha2 tests must not use v1alpha1 templates as scaffolds (see `CLAUDE.md`).

### Artifact download / S3

- Credential and anonymous-access semantics are easy to get wrong; require tests for classification (auth vs config vs source errors).

### Gateway / routing

- Multi-listener gateways and hostname pinning affect auth exposure — require tests when route construction changes.

### Generated artifacts

- Never merge hand-edited CRD/RBAC/deepcopy/helm-values reference without regenerating from source.

---

## Related documentation

- [Coding Guidelines](coding-guidelines.md) — Author checklist and day-to-day conventions (terminology, comments, standalone docs)
- [Controller Patterns](controller-patterns.md) — Reconciliation pipeline
- [Testing](testing.md) — Unit and Chainsaw e2e conventions
- [Observation Design](observation-design.md) — Fetch/observation structs
- [Release Process](release-process.md) — Changelog and tagging
- [Upgrading](../admin/upgrading.md) — Production upgrade and migration window
- [`CLAUDE.md`](../../../CLAUDE.md) — Agent-facing condensed standards
