"""Normalize POSIX permissions lost by fnpack on Windows; preserve the FPK layout."""
import argparse
import copy
import hashlib
import io
import pathlib
import re
import tarfile


def normalize_archive(raw, payload=None):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:*") as source:
        with tarfile.open(fileobj=buffer, mode="w:gz", format=tarfile.PAX_FORMAT) as target:
            for original in source.getmembers():
                if not (original.isfile() or original.isdir()):
                    raise ValueError(f"Unexpected archive member: {original.name}")
                path = pathlib.PurePosixPath(original.name)
                if path.is_absolute() or ".." in path.parts:
                    raise ValueError(f"Unsafe archive path: {original.name}")
                entry = copy.copy(original)
                entry.uid = entry.gid = 0
                entry.uname = entry.gname = ""
                entry.mode = 0o755 if entry.isdir() or entry.name.startswith("cmd/") or entry.name == "server/phicomm-manager" else 0o644
                data = source.extractfile(original).read() if entry.isfile() else None
                if payload is not None and entry.name == "app.tgz":
                    data = payload
                if payload is not None and entry.name == "manifest":
                    checksum = hashlib.md5(payload).hexdigest()
                    data, count = re.subn(rb"(?m)^(checksum\s*=\s*)[0-9a-fA-F]+", lambda m: m[1] + checksum.encode(), data)
                    if count != 1:
                        raise ValueError("FPK manifest is missing its payload checksum")
                if data is not None:
                    entry.size = len(data)
                target.addfile(entry, io.BytesIO(data) if data is not None else None)
    return buffer.getvalue()


def normalize(source, destination):
    raw = pathlib.Path(source).read_bytes()
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:*") as archive:
        payload = archive.extractfile("app.tgz").read()
    payload = normalize_archive(payload)
    pathlib.Path(destination).write_bytes(normalize_archive(raw, payload))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source")
    parser.add_argument("destination")
    args = parser.parse_args()
    normalize(args.source, args.destination)
