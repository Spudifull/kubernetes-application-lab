#!/usr/bin/env python3
"""Build a shared installer and per-node NoCloud ISOs on a Linux controller."""
import argparse
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--iso", type=pathlib.Path, required=True)
    parser.add_argument("--checksums", type=pathlib.Path, required=True)
    parser.add_argument("--nodes", type=pathlib.Path, required=True)
    parser.add_argument("--public-key", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    entries = [line.split() for line in args.checksums.read_text().splitlines()]
    expected = [line[0] for line in entries if len(line) == 2 and line[1].lstrip("*") == args.iso.name]
    if len(expected) != 1:
        raise ValueError("Expected one published checksum for this installer")
    with args.iso.open("rb") as stream:
        actual = hashlib.file_digest(stream, "sha256").hexdigest()
    if actual.lower() != expected[0].lower():
        raise ValueError("Installer checksum mismatch")
    nodes = json.loads(args.nodes.read_text(encoding="utf-8-sig"))
    if "planned_values" in nodes:
        nodes = nodes["planned_values"]["outputs"]["bootstrap"]["value"]
    if set(nodes) != {"cp", "w1", "w2"}:
        raise ValueError("Use cluster-mode Terraform bootstrap output for three nodes")
    key = args.public_key.read_text().strip()
    if not re.fullmatch(r"ssh-ed25519 [A-Za-z0-9+/=]+(?: .*)?", key):
        raise ValueError("Expected an Ed25519 public key")
    for node in nodes.values():
        if not re.fullmatch(r"kal10b-[a-z0-9]{4,16}-(cp|w1|w2)", node["hostname"]):
            raise ValueError("Unexpected hostname")
        for field in ("cluster_mac", "management_mac"):
            if not re.fullmatch(r"(?:[0-9a-f]{2}:){5}[0-9a-f]{2}", node[field]):
                raise ValueError("Invalid MAC address")
        if not re.fullmatch(r"10\.77\.10\.(11|12|13)", node["address"]):
            raise ValueError("Unexpected cluster address")
    for tool in ("xorriso", "cloud-localds"):
        if not shutil.which(tool):
            raise RuntimeError(f"Install {tool} on the Linux controller")
    if args.output.exists():
        raise FileExistsError("Output directory already exists. Select a new build directory.")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=args.output.parent) as temporary:
        work = pathlib.Path(temporary)
        result = work / "result"
        result.mkdir()
        for name, node in nodes.items():
            data = {
                "autoinstall": {
                    "version": 1,
                    "refresh-installer": {"update": False},
                    "identity": {"hostname": node["hostname"], "username": "lab", "password": "!"},
                    "ssh": {"install-server": True, "allow-pw": False, "authorized-keys": [key]},
                    "storage": {"layout": {"name": "direct"}},
                    "network": {
                        "version": 2,
                        "ethernets": {
                            "cluster": {
                                "match": {"macaddress": node["cluster_mac"]},
                                "set-name": "cluster0", "dhcp4": False,
                                "addresses": [node["address"] + "/24"],
                            },
                            "management": {
                                "match": {"macaddress": node["management_mac"]},
                                "set-name": "mgmt0", "dhcp4": True,
                            },
                        },
                    },
                    "user-data": {
                        "users": [
                            {"name": "lab", "groups": ["sudo"], "shell": "/bin/bash",
                             "lock_passwd": True, "sudo": ["ALL=(ALL) NOPASSWD:ALL"],
                             "ssh_authorized_keys": [key]}
                        ]
                    },
                    "shutdown": "poweroff",
                }
            }
            user = work / f"user-{name}"
            meta = work / f"meta-{name}"
            user.write_text("#cloud-config\n" + json.dumps(data, indent=2) + "\n")
            meta.write_text(json.dumps({"instance-id": node["hostname"], "local-hostname": node["hostname"]}))
            subprocess.run(["cloud-localds", str(result / (node["hostname"] + ".iso")), str(user), str(meta)], check=True)
        configs = []
        grub_checksums = {}
        for path in ("boot/grub/grub.cfg", "boot/grub/loopback.cfg"):
            target = work / pathlib.Path(path).name
            subprocess.run(["xorriso", "-osirrox", "on", "-indev", str(args.iso), "-extract", "/" + path, str(target)], check=True)
            text = target.read_text()
            text, count = re.subn(
                r"(?m)^(\s*linux\s+[^\n]*?/casper/vmlinuz[^\n]*)",
                lambda match: match[0].replace(" ---", " autoinstall ---")
                if " ---" in match[0] else match[0] + " autoinstall",
                text,
            )
            if not count:
                raise ValueError("No Ubuntu installer boot command found in " + path)
            target.write_text(text)
            grub_checksums["./" + path] = hashlib.md5(target.read_bytes()).hexdigest()
            configs += ["-map", str(target), "/" + path]
        md5_manifest = work / "md5sum.txt"
        subprocess.run(["xorriso", "-osirrox", "on", "-indev", str(args.iso),
                        "-extract", "/md5sum.txt", str(md5_manifest)], check=True)
        lines = []
        replaced = set()
        for line in md5_manifest.read_text().splitlines():
            fields = line.split(maxsplit=1)
            if len(fields) == 2 and fields[1] in grub_checksums:
                replaced.add(fields[1])
                line = grub_checksums[fields[1]] + "  " + fields[1]
            lines.append(line)
        if replaced != set(grub_checksums):
            raise ValueError("Installer media checksums do not contain the expected GRUB files")
        md5_manifest.write_text("\n".join(lines) + "\n")
        configs += ["-map", str(md5_manifest), "/md5sum.txt"]
        installer = result / "ubuntu-autoinstall.iso"
        subprocess.run(["xorriso", "-indev", str(args.iso), "-outdev", str(installer),
                        "-boot_image", "any", "replay", *configs], check=True)
        receipts = {"source_iso": args.iso.name, "source_sha256": actual, "generated_sha256": {}}
        for path in result.glob("*.iso"):
            with path.open("rb") as stream:
                receipts["generated_sha256"][path.name] = hashlib.file_digest(stream, "sha256").hexdigest()
        (result / "manifest.json").write_text(json.dumps(receipts, indent=2) + "\n")
        result.rename(args.output)


if __name__ == "__main__":
    main()
