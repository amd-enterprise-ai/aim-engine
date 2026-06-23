<!--
Copyright © Advanced Micro Devices, Inc., or its affiliates.

SPDX-License-Identifier: MIT
-->

# AIM Engine documentation

The published docs live under [`docs/`](docs/) and are built with
[Sphinx](https://www.sphinx-doc.org) using Markdown (via
[MyST](https://myst-parser.readthedocs.io)) and
[rocm-docs-core](https://github.com/ROCm/rocm-docs-core) — AMD's official docs
theme (pydata-sphinx-theme + sphinx-book-theme) — so local previews match the
published umbrella site at <https://enterprise-ai.docs.amd.com>. Diagrams are
authored in [D2](https://d2lang.com) and committed as rendered SVGs.

## Layout

| Path | Purpose |
| --- | --- |
| `docs/docs/` | Sphinx source root (Markdown pages, `conf.py`, `_static/`). |
| `docs/docs/README.md` | Site landing page (the `_toc.yml` root). |
| `docs/docs/_toc.yml.in` | Table of contents template; rocm-docs-core generates `_toc.yml` from it at build time. |
| `docs/docs/<section>/<section>.md` | Per-section index pages (a bullet list of the section's links, for GitHub browsing). |
| `docs/docs/assets/diagrams/*.svg` | Committed, rendered diagrams (do not edit by hand). |
| `docs/diagrams/*.d2` | D2 diagram sources. |
| `docs/diagrams/_theme.d2` | Central style shared by every diagram. |
| `docs/requirements.txt` | Python build dependencies. |

## Common tasks

Run from the repo root (tools come from `mise`):

```bash
mise exec -- make docs-serve     # live-reloading dev server (http://localhost:8000)
mise exec -- make docs-build     # one-off build, warnings treated as errors
mise exec -- make diagrams       # render all docs/diagrams/*.d2 -> committed SVGs
mise exec -- make diagrams-watch DIAGRAM=architecture-overview  # live single-diagram preview
```

## Diagrams (D2)

1. Edit or add a source file under `docs/diagrams/`. Every diagram starts with
   `...@_theme` to inherit the shared palette, classes, layout engine, and theme
   from `docs/diagrams/_theme.d2`. Restyle all graphs by editing that one file.
2. Run `make diagrams` to re-render the committed SVGs, then commit both the
   `.d2` source and the `.svg` output.
3. Reference a diagram from a page with a normal relative image so it renders in
   both Sphinx and plain GitHub browsing:

   ```markdown
   ![Alt text describing the diagram](../assets/diagrams/<name>.svg)
   ```

A pre-commit hook and the `Docs` CI workflow run `make diagrams-check` to fail if
a committed SVG drifts from its `.d2` source.

> GitHub renders Mermaid natively but not D2. The committed-SVG workflow is what
> keeps diagrams visible when browsing `.md` files directly on GitHub.

## Multi-stack / umbrella site

These docs are designed to build standalone *and* to be aggregated into the
umbrella `AMDEnterpriseAISuiteDocs` site, where the contents of `docs/docs` are
synced under `docs/aim-engine/` (see
[`.github/workflows/copy-docs-to-public-docs.yaml`](../.github/workflows/copy-docs-to-public-docs.yaml))
and built by that repo's own root `conf.py`.

To keep both modes working:

- Use **relative** `.md` links between pages — never absolute paths and never the
  `aim-engine/` prefix (the umbrella adds that when nesting this stack).
- Express navigation in `docs/docs/_toc.yml.in` (sphinx-external-toc), and give
  each section landing page a plain bullet list of the same links so GitHub
  browsing stays navigable.
- Keep page-local anchors unique per document; nesting under `aim-engine/` in the
  umbrella namespaces everything else.
