"""Verify checked-in React assets without requiring Node.js."""
import hashlib
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent


def verify():
    metadata = json.loads((ROOT / "public/build.json").read_text(encoding="utf-8"))
    sources = {"package.json", "package-lock.json", "scripts/build-web.mjs"}
    sources.update("frontend/" + file.name for file in (ROOT / "frontend").iterdir() if file.is_file())
    assets = {"public/index.html", "public/app.js", "public/styles.css"}
    assets.update(f"public/licenses/{name}.LICENSE" for name in ["react", "react-dom", "scheduler", "lucide-react"])
    if metadata.get("framework") != "React" or set(metadata["sources"]) != sources or set(metadata["assets"]) != assets:
        raise ValueError("React build metadata is outdated; run npm ci and npm run build:web")
    for group in ["sources", "assets"]:
        for name, digest in metadata[group].items():
            file = (ROOT / name).resolve()
            if ROOT not in file.parents or hashlib.sha256(file.read_bytes()).hexdigest() != digest:
                raise ValueError(f"React source/asset changed: {name}; run npm run build:web")
    print("React source and compiled assets match; no Node.js needed to build the Go binary.")


if __name__ == "__main__":
    verify()
