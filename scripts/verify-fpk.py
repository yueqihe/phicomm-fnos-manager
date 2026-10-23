"""Inspect a native manager FPK without executing its installation scripts."""
import argparse
import hashlib
import io
import json
import pathlib
import re
import tarfile
import struct


def verify_static_elf(raw, platform):
    if raw[:6] != b"\x7fELF\x02\x01":
        raise ValueError("Expected a 64-bit little-endian Linux ELF executable")
    machine = struct.unpack_from("<H", raw, 18)[0]
    if machine != {"arm": 183, "x86": 62}.get(platform):
        raise ValueError(f"ELF architecture does not match {platform}: {machine}")
    phoff = struct.unpack_from("<Q", raw, 32)[0]
    phsize, phcount = struct.unpack_from("<HH", raw, 54)
    if phsize < 56 or phoff + phsize * phcount > len(raw):
        raise ValueError("Invalid ELF program headers")
    for index in range(phcount):
        kind = struct.unpack_from("<I", raw, phoff + phsize * index)[0]
        if kind in {2, 3}:
            raise ValueError("Go binary must be static: no dynamic linker/shared-library table")


def check_members(archive):
    names = set()
    for entry in archive.getmembers():
        path = pathlib.PurePosixPath(entry.name)
        if not (entry.isfile() or entry.isdir()) or path.is_absolute() or ".." in path.parts:
            raise ValueError(f"Unsafe member: {entry.name}")
        if entry.name in names:
            raise ValueError(f"Duplicate member: {entry.name}")
        names.add(entry.name)
        if entry.mode & 0o022:
            raise ValueError(f"Group/world writable member: {entry.name}")
        if entry.name.startswith("cmd/") and not entry.mode & 0o111:
            raise ValueError(f"Non-executable hook: {entry.name}")
    return names


def verify(file, extract=None, expected_platform=None):
    with tarfile.open(file, "r:*") as archive:
        names = check_members(archive)
        required = {"app.tgz", "manifest", "ICON.PNG", "ICON_256.PNG", "cmd/main", "cmd/common", "config/privilege", "config/resource", "wizard/install"}
        if not required.issubset(names):
            raise ValueError(f"Missing files: {required - names}")
        manifest = dict(re.findall(r"(?m)^(\w+)\s*=\s*(.*?)\s*$", archive.extractfile("manifest").read().decode()))
        payload = archive.extractfile("app.tgz").read()
        if manifest.get("checksum") != hashlib.md5(payload).hexdigest():
            raise ValueError("Payload checksum does not match manifest")
        if manifest.get("platform") not in {"all", "x86", "arm"}:
            raise ValueError("Incorrect native package target/runtime")
        if expected_platform and manifest["platform"] != expected_platform:
            raise ValueError(f"Expected platform {expected_platform}, got {manifest['platform']}")
        privilege = json.load(archive.extractfile("config/privilege"))
        if privilege["defaults"]["run-as"] != "package":
            raise ValueError("Application must run as a dedicated package user")
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as app:
            app_names = check_members(app)
            if "server/phicomm-manager" in app_names:
                if manifest.get("install_dep_apps"):
                    raise ValueError("Go package must not require another runtime application")
                entry = app.getmember("server/phicomm-manager")
                if entry.mode & 0o111 != 0o111:
                    raise ValueError("Go executable is missing executable permissions")
                verify_static_elf(app.extractfile(entry).read(), manifest["platform"])
                if not {"notices/modules.json", "notices/react.LICENSE", "notices/react-dom.LICENSE", "notices/lucide-react.LICENSE", "notices/go.LICENSE"}.issubset(app_names) or any("node_modules" in name or name.startswith("server/src/") or name.startswith("server/fnos/") for name in app_names):
                    raise ValueError("Go package must contain dependency notices and no Node.js backend")
                for name in ["cmd/main", "cmd/common"]:
                    script = archive.extractfile(name).read()
                    if b"NODE_BIN" in script or b"nodejs_v22" in script or b"server.js" in script:
                        raise ValueError("Lifecycle script still depends on Node.js")
            else:
                runtime_required = {"server/src/server.js", "server/src/store.js", "server/src/discovery.js", "server/fnos/runtime/initialize.js", "server/fnos/runtime/socket.js", "server/node_modules/aedes/package.json", "server/node_modules/mqtt/package.json", "server/public/index.html", "ui/config"}
                if manifest.get("install_dep_apps") != "nodejs_v22" or not runtime_required.issubset(app_names):
                    raise ValueError("Missing legacy runtime files")
                if not {"server/src/mdns.js", "server/src/lan-scan.js", "server/node_modules/multicast-dns/index.js", "server/node_modules/dns-packet/index.js"}.issubset(app_names):
                    raise ValueError("Missing LAN discovery runtime or pure JavaScript DNS dependencies")
            for name in app_names:
                parts = pathlib.PurePosixPath(name).parts
                if any(part in {"data", "test-results", "playwright", "playwright-core", ".git"} for part in parts):
                    raise ValueError(f"Private/dev content: {name}")
                if re.search(r"\.(?:sqlite(?:-wal|-shm)?|key|log|node|exe|dll|so)$", name, re.I) or pathlib.PurePosixPath(name).name.startswith(".env"):
                    raise ValueError(f"Private/native file: {name}")
            ui = json.load(app.extractfile("ui/config"))[".url"][manifest["desktop_applaunchname"]]
            if ui["gatewayPrefix"] != "/app/phicomm-manager" or ui["gatewaySocket"] != "app.sock":
                raise ValueError("Gateway configuration does not match service")
            if extract:
                destination = pathlib.Path(extract).resolve()
                if destination.exists():
                    raise ValueError("Verification extraction requires a new directory")
                destination.mkdir(parents=True)
                app.extractall(destination, filter="data")
    print(f"Verified {manifest['appname']} {manifest['version']} ({manifest['platform']}): checksum, permissions, hooks, gateway, runtime, no private data. ({len(app_names)} payload entries)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("file")
    parser.add_argument("--extract")
    parser.add_argument("--platform", choices=["all", "x86", "arm"], help="Require this package target")
    args = parser.parse_args()
    verify(args.file, args.extract, args.platform)
