#!/usr/bin/env python3
"""Check Linux release archives for host-specific tar metadata."""
import hashlib
from pathlib import Path
import tarfile


def check(archive):
    expected = Path(str(archive) + ".sha256").read_text().split()[0]
    assert hashlib.sha256(archive.read_bytes()).hexdigest() == expected, "checksum mismatch"
    with tarfile.open(archive) as contents:
        for entry in contents:
            assert not entry.pax_headers, f"extended tar headers: {entry.name}"
            assert entry.uid == entry.gid == 0, f"host ownership: {entry.name}"
            assert entry.uname in ("", "root") and entry.gname in ("", "root"), entry.name
    print(f"OK: {archive.name}")


if __name__ == "__main__":
    archives = sorted((Path(__file__).resolve().parent.parent / "dist").glob("*.tar.gz"))
    assert archives, "build the Linux archives first"
    for archive in archives:
        check(archive)
