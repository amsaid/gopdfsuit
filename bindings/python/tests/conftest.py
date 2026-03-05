"""
Pytest configuration and fixtures for pypdfsuit tests.
"""

import os
import sys
import subprocess
from pathlib import Path

import pytest

# Add the project directory to sys.path so pypdfsuit module can be found
# during test collection without installing the package.
PYTHON_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PYTHON_DIR))


def _should_rebuild(lib_path: Path, source_roots: list[Path]) -> bool:
    if not lib_path.exists():
        return True

    lib_mtime = lib_path.stat().st_mtime
    for root in source_roots:
        if not root.exists():
            continue
        for path in root.rglob("*.go"):
            if path.stat().st_mtime > lib_mtime:
                return True
    return False


def pytest_sessionstart(session):
    """Ensure tests run against a freshly built shared library."""
    if os.getenv("PYPDFSUIT_SKIP_AUTO_BUILD") == "1":
        return

    repo_root = PYTHON_DIR.parents[1]
    lib_name = "libgopdfsuit.so"
    lib_path = PYTHON_DIR / "pypdfsuit" / "lib" / lib_name
    source_roots = [
        repo_root / "bindings" / "python" / "cgo",
        repo_root / "pkg" / "gopdflib",
        repo_root / "internal" / "pdf",
    ]

    if not _should_rebuild(lib_path, source_roots):
        return

    build_script = PYTHON_DIR / "build.sh"
    subprocess.run([str(build_script)], check=True, cwd=str(PYTHON_DIR))


@pytest.fixture
def simple_html():
    """Simple HTML content for testing."""
    return "<html><body><h1>Test</h1></body></html>"


@pytest.fixture
def simple_xfdf():
    """Simple XFDF content for testing."""
    return b"""<?xml version="1.0" encoding="UTF-8"?>
<xfdf xmlns="http://ns.adobe.com/xfdf/">
    <fields>
        <field name="Name"><value>John Doe</value></field>
        <field name="Email"><value>john@example.com</value></field>
    </fields>
</xfdf>"""
