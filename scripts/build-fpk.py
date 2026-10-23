"""Build a self-contained Go FPK; no language runtime is installed on the NAS."""
import argparse
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
# Retain the original firmware workspace layout while supporting a standalone clone.
REPO = ROOT.parent if all((ROOT.parent / name).is_dir() for name in ["TC1", "M1", "A1"]) else ROOT
VERSION = "0.2.1"


def build(platform):
    subprocess.run([sys.executable, str(ROOT / "scripts/verify-web.py")], check=True)
    arch = {"arm": "arm64", "x86": "amd64"}[platform]
    bundled_go = REPO / "build/tools/go-native/go/bin/go.exe"
    go = os.environ.get("GO_BIN") or shutil.which("go") or (str(bundled_go) if bundled_go.exists() else None)
    if not go:
        raise RuntimeError("Install Go 1.25+ on the build computer or set GO_BIN")
    bundled_fnpack = REPO / "build/tools/fnpack-1.2.3.exe"
    fnpack = os.environ.get("FNPACK") or shutil.which("fnpack") or (str(bundled_fnpack) if bundled_fnpack.exists() else None)
    if not fnpack:
        raise RuntimeError("Install fnpack 1.2.3 on the build computer or set FNPACK")
    stage = REPO / "build/fpk" / f"phicomm-manager-{VERSION}-go-{platform}-{time.time_ns()}"
    stage.mkdir(parents=True)
    template = ROOT / "fnos"
    for name in ["cmd", "config", "wizard", "app"]:
        shutil.copytree(template / name, stage / name)
    for name in ["ICON.PNG", "ICON_256.PNG"]:
        shutil.copyfile(template / name, stage / name)
    (stage / "app/icon.svg").unlink(missing_ok=True)
    manifest = (template / "manifest").read_text(encoding="utf-8")
    fields = dict(line.split("=", 1) for line in manifest.splitlines() if "=" in line)
    fields.update(version=VERSION, platform=platform)
    fields.pop("install_dep_apps", None)
    (stage / "manifest").write_text("".join(f"{key}={value}\n" for key, value in fields.items()), encoding="utf-8")
    server = stage / "app/server"
    server.mkdir()
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH=arch, GOTOOLCHAIN="local")
    subprocess.run([go, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(server / "phicomm-manager"), "."], cwd=ROOT, env=env, check=True)
    # Redistribute the licenses of the dependencies compiled into the executable.
    raw = subprocess.check_output([go, "list", "-m", "-json", "all"], cwd=ROOT, env=env).decode()
    decoder = json.JSONDecoder()
    modules = []
    while raw.strip():
        module, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        if not module.get("Main"):
            modules.append(module)
    notices = stage / "app/notices"
    notices.mkdir()
    shutil.copyfile(ROOT / "THIRD_PARTY_NOTICES.md", notices / "THIRD_PARTY_NOTICES.md")
    for license_file in (ROOT / "public/licenses").iterdir():
        shutil.copyfile(license_file, notices / license_file.name)
    go_root = pathlib.Path(subprocess.check_output([go, "env", "GOROOT"], cwd=ROOT, env=env).decode().strip())
    shutil.copyfile(go_root / "LICENSE", notices / "go.LICENSE")
    metadata = []
    for module in modules:
        metadata.append({key: module.get(key) for key in ["Path", "Version", "Sum"]})
        directory = pathlib.Path(module["Dir"]) if "Dir" in module else None
        if directory and directory.exists():
            for file in directory.iterdir():
                if file.is_file() and file.name.upper().startswith(("LICENSE", "COPYING", "NOTICE")):
                    destination = notices / module["Path"].replace("/", "_")
                    destination.mkdir(exist_ok=True)
                    shutil.copyfile(file, destination / file.name)
    (notices / "modules.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    for file in stage.rglob("*"):
        if file.is_file() and file.parts[-2] == "cmd":
            file.write_text(file.read_text(encoding="utf-8"), encoding="utf-8", newline="\n")
    output = REPO / "release/phicomm-manager"
    output.mkdir(parents=True, exist_ok=True)
    raw_output = stage.parent / (stage.name + "-output")
    raw_output.mkdir()
    subprocess.run([fnpack, "build", "--directory", str(stage)], cwd=raw_output, check=True)
    package = output / f"phicomm-manager-{VERSION}-{platform}.fpk"
    subprocess.run([sys.executable, str(ROOT / "scripts/normalize-fpk.py"), str(raw_output / "phicomm-manager.fpk"), str(package)], check=True)
    subprocess.run([sys.executable, str(ROOT / "scripts/verify-fpk.py"), str(package), "--platform", platform], check=True)
    digest = hashlib.sha256(package.read_bytes()).hexdigest()
    package.with_suffix(".fpk.sha256").write_text(f"{digest}  {package.name}\n", encoding="utf-8")
    info = dict(appname="phicomm-manager", version=VERSION, platform=platform, architecture=arch, runtime="self-contained Go (CGO_ENABLED=0)", stage=str(stage), package=package.name, sha256=digest)
    for filename in ["build-info.json", f"build-info-{platform}.json"]:
        (output / filename).write_text(json.dumps(info, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    shutil.copyfile(template / "INSTALL.md", output / "安装说明.md")
    print(f"FPK: {package}\nSHA256: {digest}\nStage: {stage}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=["arm", "x86"], default="arm")
    build(parser.parse_args().platform)
