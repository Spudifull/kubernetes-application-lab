#!/usr/bin/env bash
set -euo pipefail
out=$1
group=$2
jq -e --arg group "$group" '
  .group == $group and (.sha | test("^[0-9a-f]{40}$")) and
  (.cluster | startswith("lab-mn-")) and .namespace == "multinode" and
  (.kind | contains("v0.33.0")) and
  (.run == "" or ((.run | test("^[0-9]+$")) and (.url | endswith("/actions/runs/" + .run))))
' "$out/run.json"
jq -e '.serverVersion.gitVersion == "v1.34.0"' "$out/kubernetes.json"
jq -e '.cpu > 0 and .memoryKiB > 0' "$out/runner.json"
jq -e '.threeReadyNodes and .twoReadyReplicasOnDifferentWorkers and .serviceHTTP and .revisionMatches' "$out/bootstrap-assertions.json"
jq -e '.exitCode == 0' "$out/scenario-status.json"
jq -e --slurpfile run "$out/run.json" '.deleted == true and .cluster == $run[0].cluster' "$out/cleanup.json"
case "$group" in
  bootstrap) ;;
  scheduler)
    jq -es 'length == 2 and all(.[]; .phase=="Pending" and .node==null)' "$out/affinity-missing.json" "$out/taint-blocked.json"
    jq -es 'length == 6 and all(.[]; (.uid | length)>0 and (.node | contains("-worker")))' "$out"/{selector-one,selector-two,affinity-required,affinity-preferred,affinity-recovered,taint-allowed}.json
    ;;
  maintenance)
    jq -e '. == true' "$out/cordon.json" "$out/uncordon.json"
    grep -qi 'disruption budget' "$out/pdb-eviction.txt"
    test -s "$out/http-during-drain.txt" && test -s "$out/http-after-drain.txt"
    ;;
  outage)
    jq -e '.ready == 2 and .generation == .observed' "$out/outage-replaced-deployment.json"
    jq -e 'length == 3 and all(.[]; any(.conditions[]; .status == "True"))' "$out/outage-recovered-nodes.json"
    ;;
  network)
    jq -es 'length == 3 and all(.[]; .expected=="deny" and (.result | test("^000:(7|28)$")))' "$out"/{default-deny,blocked-deny,blocked-client}.json
    jq -es 'length == 3 and all(.[]; .expected=="allow" and .result=="200:0")' "$out"/{baseline,allowed-client,restored}.json
    ;;
  *) exit 1 ;;
esac
