#!/bin/bash

# Sets up the Python environment and serves the Sphinx docs locally with live
# reload. Prefer `make docs-serve` from the repo root; this script is a
# standalone equivalent.

set -e

VENV_DIR=".venv"
PORT="${DOCS_PORT:-8000}"

if [ ! -d "$VENV_DIR" ]; then
    echo "Creating virtual environment..."
    python3 -m venv "$VENV_DIR"
fi

echo "Activating virtual environment..."
source "$VENV_DIR/bin/activate"

echo "Installing dependencies from requirements.txt..."
pip install -q -r requirements.txt

echo "Starting Sphinx dev server on http://localhost:${PORT}/ ..."
echo "Tip: in another terminal, run 'make diagrams-watch DIAGRAM=<name>' for live diagram editing."
sphinx-autobuild docs docs/_build/html --host 0.0.0.0 --port "$PORT"
