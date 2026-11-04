#!/usr/bin/env python3
"""Reject unrelated objects and replacement/deletion in a normal saved plan."""
import argparse
import json
import pathlib
import re

parser = argparse.ArgumentParser()
parser.add_argument("plan", type=pathlib.Path)
parser.add_argument("--prefix", required=True)
parser.add_argument("--destroy", action="store_true")
args = parser.parse_args()
if not re.fullmatch(r"kal10b-[a-z0-9]{4,16}", args.prefix):
    raise ValueError("Invalid project prefix")
plan = json.loads(args.plan.read_text(encoding="utf-8-sig"))
if plan.get("errored") or plan.get("deferred_changes"):
    raise ValueError("Incomplete or errored plans cannot be applied")
variables = plan["variables"]
if variables["prefix"]["value"] != args.prefix:
    raise ValueError("Plan prefix differs from reviewed prefix")
root = variables["storage_root"]["value"].replace("\\", "/").rstrip("/")
directory = root + "/" + args.prefix
mode = variables["mode"]["value"]
suffixes = ["probe"] if mode == "probe" else ["cp", "w1", "w2"]
names = {args.prefix + "-" + suffix for suffix in suffixes}
changes = plan.get("resource_changes", [])
expected_count = 1 + 2 * len(names)
if len(changes) != expected_count:
    raise ValueError("Plan does not describe the complete expected resource set")
for resource in changes:
    change = resource["change"]
    actions = change["actions"]
    if args.destroy:
        if actions not in (["delete"], ["no-op"]):
            raise ValueError("Unexpected action in destroy plan")
    elif actions not in (["create"], ["update"], ["no-op"]):
        raise ValueError("Deletion and replacement need a separately reviewed plan")
    values = change["before"] if actions == ["delete"] else change["after"]
    resource_type = resource["type"]
    if resource_type == "hyperv_network_switch":
        if values["name"] != args.prefix + "-net" or values["switch_type"] != "Private":
            raise ValueError("Unexpected switch")
    elif resource_type == "hyperv_vhd":
        path = values["path"].replace("\\", "/")
        if path not in {directory + "/" + name + ".vhdx" for name in names}:
            raise ValueError("Disk lies outside expected owned paths")
    elif resource_type == "hyperv_machine_instance":
        if values["name"] not in names or values["path"].replace("\\", "/") != directory:
            raise ValueError("VM is outside the project inventory")
    else:
        raise ValueError("Unexpected managed resource " + resource_type)
print("Saved plan ownership and actions checked for " + args.prefix)
