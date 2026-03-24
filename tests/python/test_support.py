from __future__ import annotations

import importlib.util
import os
import uuid
from pathlib import Path


def repo_root() -> Path:
    override = os.environ.get("WORKCELL_MUTATION_ROOT")
    if override:
        return Path(override).resolve()
    return Path(__file__).resolve().parents[2]


def load_module(relative_path: str):
    path = repo_root() / relative_path
    spec = importlib.util.spec_from_file_location(
        f"workcell_test_{uuid.uuid4().hex}", path
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load module from {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module
