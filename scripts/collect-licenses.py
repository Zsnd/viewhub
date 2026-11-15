"""Refresh bundled notices from installed, locked dependencies (no downloads).

Run after npm ci in both apps and go mod download:
  python scripts/collect-licenses.py --go /path/to/go
The separately preserved shadcn-ui.txt covers generated UI component sources.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def json_stream(text):
    decoder = json.JSONDecoder()
    at = 0
    while at < len(text):
        while at < len(text) and text[at].isspace():
            at += 1
        if at == len(text):
            break
        value, at = decoder.raw_decode(text, at)
        yield value


def notices(directory):
    files = []
    for path in sorted(directory.iterdir()):
        if path.name.lower().startswith(("license", "licence", "copying", "notice", "copyright")):
            if path.is_file():
                files.append(path)
            elif path.is_dir():
                files.extend(p for p in sorted(path.rglob("*")) if p.is_file())
    return files


def notice_text(path):
    # Normalize line endings/trailing spaces only; retain every license clause.
    return "\n".join(line.rstrip() for line in path.read_text(encoding="utf-8", errors="replace").splitlines()).strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--go", default="go")
    args = parser.parse_args()
    texts = {}
    packages = {}
    missing = []
    upstream = json.loads((ROOT / "licenses/upstream-sources.json").read_text(encoding="utf-8"))

    def add(name, directory):
        paths = notices(directory)
        if not paths:
            if name in upstream:
                source = upstream[name]
                text = notice_text(ROOT / "licenses" / source["file"])
                digest = hashlib.sha256(text.encode()).hexdigest()[:16]
                texts[digest] = text
                packages[name] = [source["file"] + ": " + digest, source["url"]]
                return
            missing.append(name)
            return
        references = []
        for path in paths:
            text = notice_text(path)
            digest = hashlib.sha256(text.encode()).hexdigest()[:16]
            texts[digest] = text
            references.append(str(path.relative_to(directory)) + ": " + digest)
        packages[name] = sorted(set(references))

    result = subprocess.check_output(
        [args.go, "list", "-deps", "-json", "./apps/platform/cmd/gateway", "./apps/capture/cmd/capture"],
        cwd=ROOT, text=True, encoding="utf-8",
    )
    modules = {}
    for package in json_stream(result):
        module = package.get("Module", {})
        if module.get("Version") and module.get("Dir"):
            modules[module["Path"] + "@" + module["Version"]] = Path(module["Dir"])
    for name, directory in sorted(modules.items()):
        add("Go " + name, directory)
    goroot = subprocess.check_output([args.go, "env", "GOROOT"], text=True).strip()
    add("Go standard library", Path(goroot))

    for app in ("platform", "capture"):
        web = ROOT / "apps" / app / "web"
        lock = json.loads((web / "package-lock.json").read_text(encoding="utf-8"))
        for relative, metadata in sorted(lock["packages"].items()):
            directory = web / relative
            if not relative or not directory.is_dir():
                continue
            manifest = directory / "package.json"
            if not manifest.is_file():
                continue
            package = json.loads(manifest.read_text(encoding="utf-8"))
            name = package.get("name", relative) + "@" + metadata.get("version", "unknown")
            add("npm " + name, directory)

    if missing:
        raise SystemExit("Missing upstream license text; review before distributing: " + ", ".join(sorted(set(missing))))
    lines = [
        "ViewHub third-party notices", "",
        "ViewHub's MIT license applies to its own code. Third-party code, fonts and",
        "components retain their original licenses and copyrights, reproduced below.",
        "Installed build-time dependencies are also included conservatively.",
        "FFmpeg is installed separately and is not distributed with ViewHub.",
        "Generated shadcn/ui component notices are preserved in shadcn-ui.txt.", "",
        "DEPENDENCY INDEX", "",
    ]
    for name, references in sorted(packages.items()):
        lines.extend([name, *["  " + ref for ref in references], ""])
    for digest, text in sorted(texts.items()):
        lines.extend(["=" * 72, "LICENSE TEXT " + digest, "=" * 72, text, ""])
    output = ROOT / "licenses"
    output.mkdir(exist_ok=True)
    (output / "THIRD_PARTY_NOTICES.txt").write_text("\n".join(lines), encoding="utf-8")
    print(f"Collected {len(packages)} dependency entries, {len(texts)} distinct notice texts")


if __name__ == "__main__":
    main()
