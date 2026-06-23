# Copyright © Advanced Micro Devices, Inc., or its affiliates.
#
# SPDX-License-Identifier: MIT
"""Sphinx configuration for the AIM Engine documentation.

Built with rocm-docs-core (AMD's official docs theme: pydata-sphinx-theme +
sphinx-book-theme + MyST + sphinx-design + sphinx-external-toc), so the local
preview matches the published umbrella site at
https://enterprise-ai.docs.amd.com.

This project also builds standalone and is aggregated into the umbrella
AMDEnterpriseAISuiteDocs site, where the contents of ``docs/docs`` are rsynced
under ``docs/aim-engine/`` (see
``.github/workflows/copy-docs-to-public-docs.yaml``) and built by that repo's
own config. To keep both modes working: all internal links are relative ``.md``
links, the table of contents lives in ``_toc.yml``, and nothing hardcodes the
``aim-engine/`` prefix that only exists in the umbrella site.
"""

import os
from datetime import datetime

# -- Project information ------------------------------------------------------
# Parameterized via env vars so the umbrella build can override per stack.
project = os.environ.get("DOCS_PROJECT", "AIM Engine")
author = "Advanced Micro Devices, Inc."
copyright = f"{datetime.now():%Y}, {author}"
version = os.environ.get("DOCS_VERSION", "")
release = version

# -- rocm-docs-core ----------------------------------------------------------
extensions = ["rocm_docs"]
html_theme = "rocm_docs_theme"
# "generic" gives the AMD-branded theme without the ROCm-product header/banner
# (and avoids the ROCm version banner network fetch). Other flavors: rocm,
# instinct, local, ...
html_theme_options = {
    "flavor": "generic",
    # The generic-flavor header shows "GitHub" and "Support" links; without a
    # repository_url they fall back to "#". Point them at the repo and its
    # new-issue page.
    "repository_url": "https://github.com/amd-enterprise-ai/aim-engine",
}

# Table of contents (sphinx-external-toc, configured by rocm-docs-core).
external_toc_path = "_toc.yml"

# Don't fetch the shared ROCm projects.yaml over the network at build time; this
# project doesn't use cross-project intersphinx.
external_projects_remote_repository = ""
external_projects = []

# -- MyST --------------------------------------------------------------------
# rocm-docs-core already enables colon_fence, substitution, etc.; this list is
# unioned with those. Deep heading anchors so the generated CRD API reference's
# in-page anchor links resolve.
# A set, because rocm-docs-core unions it with the extensions it already enables
# (colon_fence, substitution, html_image, ...).
myst_enable_extensions = {
    "linkify",
    "deflist",
    "tasklist",
    "attrs_inline",
    "attrs_block",
}
myst_heading_anchors = 6

# The generated CRD API reference (reference/api/*.md) links between types with
# same-page anchors that MyST renders as working links but still flags as
# false-positive xref_missing warnings. Suppress that category so ``-W`` builds
# stay meaningful for real problems.
suppress_warnings = ["myst.xref_missing"]

# -- HTML output -------------------------------------------------------------
html_title = project
html_static_path = ["_static"]
html_css_files = ["extra.css"]
# Root index.html that redirects to README.html (the landing page builds as
# README.html since the _toc.yml root is README, kept so GitHub renders the
# folder README) so the site root resolves.
html_extra_path = ["_extra"]

# Contributing docs are maintainer-internal and not part of the published site.
exclude_patterns = ["contributing/*"]


def _copy_assets(app, exception):
    """Copy assets/ to the output verbatim, preserving the "assets/diagrams/..."
    path the pages reference (the same path GitHub uses). Diagrams are embedded
    with raw-HTML <picture> elements for prefers-color-scheme light/dark
    switching, which bypasses Sphinx's normal image collection.
    """
    import os
    import shutil

    if exception is not None or app.builder.name != "html":
        return
    src = os.path.join(app.srcdir, "assets")
    if os.path.isdir(src):
        shutil.copytree(src, os.path.join(app.outdir, "assets"), dirs_exist_ok=True)


def setup(app):
    import logging

    # Filter rocm-docs-core's benign "current project not found in projects"
    # warning so it doesn't fail strict (-W) builds. This project isn't a member
    # of the shared ROCm projects.yaml and doesn't use cross-project intersphinx.
    class _DropProjectsWarning(logging.Filter):
        def filter(self, record: logging.LogRecord) -> bool:
            return "not found in projects" not in record.getMessage()

    logging.getLogger("sphinx.rocm_docs.projects").addFilter(_DropProjectsWarning())

    app.connect("build-finished", _copy_assets)
