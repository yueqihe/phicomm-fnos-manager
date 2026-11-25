"""Build a self-contained Go FPK; no language runtime is installed on the NAS."""
import argparse
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tarfile
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
# Retain the original firmware workspace layout while supporting a standalone clone.
REPO = ROOT.parent if all((ROOT.parent / name).is_dir() for name in ["TC1", "M1", "A1"]) else ROOT
VERSION = "0.2.16"


def stage_mode(name, is_directory):
    return 0o755 if is_directory or name.startswith("cmd/") or name == "app/server/phicomm-manager" else 0o644


def pack(stage, output, fnpack, key, certificate, unsigned, wsl_distro):
    signing = ["--nosign"] if unsigned else (["--key", str(key), "--cert", str(certificate)] if key else [])
    if wsl_distro:
        def linux_path(file):
            return subprocess.check_output(["wsl", "-d", wsl_distro, "--exec", "wslpath", "-u", str(pathlib.Path(file).resolve())], encoding="utf-8").strip()

        # Extract on the Linux filesystem so executable permissions are set BEFORE signing.
        bundle = output / "source.tar"
        with tarfile.open(bundle, "w", format=tarfile.PAX_FORMAT) as archive:
            for file in sorted(stage.rglob("*")):
                if file.is_symlink() or not (file.is_file() or file.is_dir()):
                    raise ValueError(f"Unsupported staging entry: {file}")
                name = file.relative_to(stage).as_posix()
                entry = archive.gettarinfo(str(file), arcname=name)
                entry.uid = entry.gid = 0
                entry.uname = entry.gname = ""
                entry.mode = stage_mode(name, file.is_dir())
                if file.is_file():
                    with file.open("rb") as source:
                        archive.addfile(entry, source)
                else:
                    archive.addfile(entry)
        signing = ["--nosign"] if unsigned else (["--key", linux_path(key), "--cert", linux_path(certificate)] if key else [])
        script = '''set -eu
scratch=$(mktemp -d /tmp/iot-fpk-XXXXXX)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/stage"
tar -xf "$1" -C "$scratch/stage"
cp "$2" "$scratch/fnpack"
chmod 755 "$scratch/fnpack"
destination=$3
shift 3
cd "$scratch"
./fnpack build --directory "$scratch/stage" "$@"
test -s phicomm-manager.fpk
cp phicomm-manager.fpk "$destination"
'''
        subprocess.run(["wsl", "-d", wsl_distro, "--exec", "sh", "-c", script, "pack", linux_path(bundle), linux_path(fnpack), linux_path(output / "phicomm-manager.fpk"), *signing], check=True)
    else:
        for file in stage.rglob("*"):
            file.chmod(stage_mode(file.relative_to(stage).as_posix(), file.is_dir()))
        subprocess.run([fnpack, "build", "--directory", str(stage), *signing], cwd=output, check=True)
    if not (output / "phicomm-manager.fpk").is_file():
        raise RuntimeError("fnpack did not produce an FPK (it can report a failure with exit code zero)")


def build(platform, key=None, certificate=None, unsigned=False, wsl_distro=None):
    if bool(key) != bool(certificate):
        raise ValueError("Specify both --key and --cert, or use the credentials imported into fnpack")
    if unsigned and (key or certificate):
        raise ValueError("--nosign cannot be combined with signing credentials")
    key = pathlib.Path(key).resolve() if key else None
    certificate = pathlib.Path(certificate).resolve() if certificate else None
    for file in [key, certificate]:
        if file and not file.is_file():
            raise ValueError(f"Signing credential not found: {file}")
    if os.name == "nt" and not unsigned and not wsl_distro:
        raise ValueError("Signed builds on Windows require --wsl-distro and a Linux fnpack 1.3.3+ binary via FNPACK; signing must follow POSIX permission setup")
    subprocess.run([sys.executable, str(ROOT / "scripts/verify-web.py")], check=True)
    arch = {"arm": "arm64", "x86": "amd64"}[platform]
    bundled_go = REPO / "build/tools/go-native/go/bin/go.exe"
    go = os.environ.get("GO_BIN") or shutil.which("go") or (str(bundled_go) if bundled_go.exists() else None)
    if not go:
        raise RuntimeError("Install Go 1.25+ on the build computer or set GO_BIN")
    bundled_fnpack = REPO / ("build/tools/fnpack-1.3.3-linux-amd64" if wsl_distro else "build/tools/fnpack-1.3.3.exe")
    fnpack = os.environ.get("FNPACK") or shutil.which("fnpack") or (str(bundled_fnpack) if bundled_fnpack.exists() else None)
    if not fnpack:
        raise RuntimeError("Install fnpack 1.3.3+ on the build computer or set FNPACK")
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
    pack(stage, raw_output, fnpack, key, certificate, unsigned, wsl_distro)
    suffix = "-unsigned" if unsigned else ""
    package = output / f"phicomm-manager-{VERSION}-{platform}{suffix}.fpk"
    raw_package = raw_output / "phicomm-manager.fpk"
    if unsigned and os.name == "nt" and not wsl_distro:
        subprocess.run([sys.executable, str(ROOT / "scripts/normalize-fpk.py"), str(raw_package), str(package)], check=True)
    else:
        # A signed FPK is final. Repacking it would invalidate the signed checksums.
        shutil.copyfile(raw_package, package)
    verification = ["--require-signature"] if not unsigned else []
    if certificate:
        verification += ["--certificate", str(certificate)]
    subprocess.run([sys.executable, str(ROOT / "scripts/verify-fpk.py"), str(package), "--platform", platform, *verification], check=True)
    digest = hashlib.sha256(package.read_bytes()).hexdigest()
    package.with_suffix(".fpk.sha256").write_text(f"{digest}  {package.name}\n", encoding="utf-8")
    info = dict(appname="phicomm-manager", version=VERSION, platform=platform, architecture=arch, runtime="self-contained Go (CGO_ENABLED=0)", signed=not unsigned, stage=str(stage), package=package.name, sha256=digest)
    for filename in ["build-info.json", f"build-info-{platform}.json"]:
        (output / filename).write_text(json.dumps(info, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    shutil.copyfile(template / "INSTALL.md", output / "安装说明.md")
    print(f"FPK: {package}\nSHA256: {digest}\nStage: {stage}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=["arm", "x86"], default="arm")
    parser.add_argument("--key", default=os.environ.get("FNPACK_KEY"), help="Signing private key outside the repository")
    parser.add_argument("--cert", default=os.environ.get("FNPACK_CERT"), help="Developer certificate outside the repository")
    parser.add_argument("--wsl-distro", default=os.environ.get("FNPACK_WSL_DISTRO"), help="Windows: package on this WSL distribution with Linux fnpack")
    parser.add_argument("--nosign", action="store_true", help="Unsigned local debugging build, never a release artifact")
    args = parser.parse_args()
    build(args.platform, args.key, args.cert, args.nosign, args.wsl_distro)
