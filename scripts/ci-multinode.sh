#!/usr/bin/env bash
set -euo pipefail
: "${IMAGE:?}" "${REVISION:?}"
cluster="lab-mn-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$"
ns=multinode
group=${GROUP:-bootstrap}
worker1="$cluster-worker"
worker2="$cluster-worker2"
config=cluster/multi-node/kind.yaml
cni=kindnet
if [[ "$group" == network ]]; then
  config=cluster/multi-node/kind-calico.yaml
  cni=calico-v3.33.0
fi
out=".cache/multinode/evidence/$cluster"
mkdir -p "$out"
work=$(mktemp -d)
export KUBECONFIG="$work/config"

k() { kubectl --context "kind-$cluster" --namespace "$ns" --request-timeout=30s "$@"; }
snapshot() {
  k get nodes -o json | jq '[.items[] | {name:.metadata.name, uid:.metadata.uid, unschedulable:(.spec.unschedulable // false), conditions:[.status.conditions[] | select(.type=="Ready") | {type,status,reason}]}]' > "$out/$1-nodes.json"
  k get pods -l app.kubernetes.io/instance=multinode-api -o json | jq '[.items[] | {name:.metadata.name, uid:.metadata.uid, node:.spec.nodeName, deleting:(.metadata.deletionTimestamp != null), phase:.status.phase, conditions:[.status.conditions[]? | {type,status,reason,message}]}]' > "$out/$1-pods.json"
}
existing=$(kind get clusters)
if grep -Fxq "$cluster" <<< "$existing"; then
  echo "Cluster name already in use: $cluster" >&2
  exit 1
fi
cleanup() {
  status=$?
  trap - EXIT
  snapshot final || true
  printf '{"exitCode":%s}\n' "$status" > "$out/scenario-status.json"
  k get events -o json | jq '[.items[] | {reason,message,type,object:.involvedObject.name}]' > "$out/events.json" || true
  if kind delete cluster --name "$cluster" && remaining=$(kind get clusters) && ! grep -Fxq "$cluster" <<< "$remaining"; then
    jq -n --arg cluster "$cluster" '{cluster:$cluster,deleted:true}' > "$out/cleanup.json"
  else
    jq -n --arg cluster "$cluster" '{cluster:$cluster,deleted:false}' > "$out/cleanup.json"
    status=1
  fi
  rm -f -- "$KUBECONFIG"
  rmdir -- "$work"
  if [[ "$status" == 0 ]]; then bash scripts/ci-multinode-evidence.sh "$out" "$group" || status=1; fi
  exit "$status"
}
trap cleanup EXIT
jq -n --arg sha "$REVISION" --arg cluster "$cluster" --arg namespace "$ns" \
  --arg run "${GITHUB_RUN_ID:-}" --arg url "${GITHUB_SERVER_URL:-}/${GITHUB_REPOSITORY:-}/actions/runs/${GITHUB_RUN_ID:-}" \
  --arg group "$group" --arg cni "$cni" --arg kind "$(kind version)" '{sha:$sha,cluster:$cluster,namespace:$namespace,run:$run,url:$url,kind:$kind,group:$group,cni:$cni}' > "$out/run.json"
cp "$config" cluster/multi-node/values-api.yaml "$out/"
if [[ "$group" == network ]]; then cp cluster/multi-node/{default-deny,allow-client}.yaml "$out/"; fi
jq -n --argjson cpu "$(nproc)" --argjson memoryKiB "$(awk '/MemTotal/ {print $2}' /proc/meminfo)" '{cpu:$cpu,memoryKiB:$memoryKiB}' > "$out/runner.json"
kind create cluster --name "$cluster" --config "$config" \
  --image kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a --wait 0s
if [[ "$group" == network ]]; then
  k get --raw /apis/admissionregistration.k8s.io/v1beta1 | jq -e '
    [.resources[].kind] | contains(["MutatingAdmissionPolicy", "MutatingAdmissionPolicyBinding"])
  ' > "$out/calico-admission-api.json"
  k create -f https://raw.githubusercontent.com/projectcalico/calico/v3.33.0/manifests/v3_projectcalico_org-v1beta1.yaml
  k -n kube-system create -f https://raw.githubusercontent.com/projectcalico/calico/v3.33.0/manifests/calico.yaml
  k -n kube-system rollout status daemonset/calico-node --timeout=360s
fi
k wait --for=condition=Ready node --all --timeout=180s
k version -o json > "$out/kubernetes.json"
kind load docker-image "$IMAGE" --name "$cluster"
helm upgrade --install multinode-api helm/lab-api -f cluster/multi-node/values-api.yaml \
  --namespace "$ns" --create-namespace --kube-context "kind-$cluster" \
  --set-string image.repository="${IMAGE%:*}" --set-string image.tag="${IMAGE##*:}" --wait --timeout 180s
k run smoke --image=curlimages/curl:8.16.0 --restart=Never --labels=client=allowed --overrides="$(jq -nc --arg node "$worker2" '{spec:{nodeSelector:{"kubernetes.io/hostname":$node}}}')" --command -- sleep 900
k wait --for=condition=Ready pod/smoke --timeout=120s
k exec smoke -- curl -fsS --max-time 5 http://multinode-api.multinode.svc/readyz > "$out/readyz.txt"
k exec smoke -- curl -fsS --max-time 5 http://multinode-api.multinode.svc/version > "$out/version.json"
snapshot bootstrap
jq -e --arg revision "$REVISION" '.revision == $revision' "$out/version.json"
jq -e --arg cluster "$cluster" --slurpfile nodes "$out/bootstrap-nodes.json" '
  ($nodes[0] | length == 3 and all(.[]; any(.conditions[]; .status == "True"))) and
  length == 2 and all(.[]; any(.conditions[]; .type == "Ready" and .status == "True")) and
  ([.[].node] | sort) == [$cluster + "-worker", $cluster + "-worker2"]
' "$out/bootstrap-pods.json"
jq -n '{threeReadyNodes:true,twoReadyReplicasOnDifferentWorkers:true,serviceHTTP:true,revisionMatches:true}' > "$out/bootstrap-assertions.json"
request() {
  k exec smoke -- curl -fsS --max-time 5 http://multinode-api.multinode.svc/readyz
}
place() {
  local name=$1 expected=$2 overrides=$3
  k run "$name" --restart=Never --image=curlimages/curl:8.16.0 --overrides="$overrides" --command -- sleep 900
  k wait --for=condition=Ready "pod/$name" --timeout=90s
  k get pod "$name" -o json | jq -e --arg node "$expected" '{uid:.metadata.uid,node:.spec.nodeName} | select(.node == $node)' > "$out/$name.json"
}
selector() { jq -nc --arg worker "$1" '{spec:{nodeSelector:{"lab.example/worker":$worker}}}'; }
scheduler() {
  k label node "$worker1" lab.example/worker=one
  k label node "$worker2" lab.example/worker=two
  place selector-one "$worker1" "$(selector one)"
  place selector-two "$worker2" "$(selector two)"
  local term required preferred
  term='{"matchExpressions":[{"key":"lab.example/worker","operator":"In","values":["two"]}]}'
  required=$(jq -nc --argjson term "$term" '{spec:{affinity:{nodeAffinity:{requiredDuringSchedulingIgnoredDuringExecution:{nodeSelectorTerms:[$term]}}}}}')
  preferred=$(jq -nc --argjson term "$term" '{spec:{affinity:{nodeAffinity:{preferredDuringSchedulingIgnoredDuringExecution:[{weight:100,preference:$term}]}}}}')
  # Leave both workers eligible so the preferred rule actually decides placement.
  place affinity-required "$worker2" "$required"
  place affinity-preferred "$worker2" "$preferred"
  pending affinity-missing "$(jq -c '.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].values=["missing"]' <<< "$required")" 'affinity|selector'
  k label node "$worker2" lab.example/worker=missing --overwrite
  k wait --for=condition=Ready pod/affinity-missing --timeout=90s
  k get pod affinity-missing -o json | jq -e --arg node "$worker2" '{uid:.metadata.uid,node:.spec.nodeName} | select(.node==$node)' > "$out/affinity-recovered.json"
  jq -e --slurpfile before "$out/affinity-missing.json" '.uid == $before[0].uid' "$out/affinity-recovered.json"
  k label node "$worker2" lab.example/worker=two --overwrite
  k taint node "$worker1" lab.example/experiment=reserved:NoSchedule
  pending taint-blocked "$(selector one)" 'untolerated taint'
  place taint-allowed "$worker1" "$(selector one | jq -c '.spec.tolerations=[{key:"lab.example/experiment",operator:"Equal",value:"reserved",effect:"NoSchedule"}]')"
  k taint node "$worker1" lab.example/experiment:NoSchedule-
}
pending() {
  local name=$1 overrides=$2 reason=$3
  k run "$name" --restart=Never --image=curlimages/curl:8.16.0 --overrides="$overrides" --command -- sleep 900
  k wait --for=condition=PodScheduled=false "pod/$name" --timeout=90s
  k get pod "$name" -o json | jq -e --arg reason "$reason" '
    {uid:.metadata.uid,node:.spec.nodeName,phase:.status.phase,conditions:.status.conditions}
    | select(.phase=="Pending" and .node==null and any(.conditions[]; .reason=="Unschedulable" and (.message|test($reason))))
  ' > "$out/$name.json"
}
maintenance() {
  snapshot before-drain
  k cordon "$worker1"
  k get node "$worker1" -o json | jq -e '.spec.unschedulable' > "$out/cordon.json"
  place cordon-probe "$worker2" '{}'
  k delete pod cordon-probe --wait=true
  # Deny an eviction with a strict budget, then permit one voluntary disruption.
  printf '%s\n' '{"apiVersion":"policy/v1","kind":"PodDisruptionBudget","metadata":{"name":"api"},"spec":{"minAvailable":2,"selector":{"matchLabels":{"app.kubernetes.io/instance":"multinode-api"}}}}' | k apply -f -
  k wait --for=jsonpath='{.status.currentHealthy}'=2 pdb/api --timeout=90s
  local target
  target=$(jq -er --arg node "$worker1" '.[] | select(.node==$node) | .name' "$out/before-drain-pods.json")
  if printf '{"apiVersion":"policy/v1","kind":"Eviction","metadata":{"name":"%s","namespace":"%s"}}\n' "$target" "$ns" |
    k create --raw "/api/v1/namespaces/$ns/pods/$target/eviction" -f - > "$out/pdb-eviction.txt" 2>&1; then
    echo 'Strict PDB unexpectedly permitted eviction' >&2
    return 1
  fi
  grep -qi 'disruption budget' "$out/pdb-eviction.txt"
  k patch pdb api --type=merge -p '{"spec":{"minAvailable":1}}'
  k wait --for=jsonpath='{.status.disruptionsAllowed}'=1 pdb/api --timeout=90s
  k drain "$worker1" --ignore-daemonsets --delete-emptydir-data --timeout=180s > "$out/drain.txt" 2>&1 &
  local drain_pid=$!
  if ! request > "$out/http-during-drain.txt"; then
    wait "$drain_pid" || true
    return 1
  fi
  wait "$drain_pid"
  k rollout status deployment/multinode-api --timeout=120s
  snapshot drained
  jq -e --arg node "$worker2" --arg target "$target" 'length==2 and all(.[]; .node==$node and .name!=$target)' "$out/drained-pods.json"
  request > "$out/http-after-drain.txt"
  k uncordon "$worker1"
  k get node "$worker1" -o json | jq -e '(.spec.unschedulable // false) == false' > "$out/uncordon.json"
  place uncordon-probe "$worker1" "$(selector one)"
  k delete pod uncordon-probe --wait=true
}
deployment() {
  k get deployment multinode-api -o json | jq '{generation:.metadata.generation,observed:.status.observedGeneration,ready:.status.readyReplicas}'
}
outage() {
  snapshot before-outage
  deployment > "$out/before-outage-deployment.json"
  docker inspect --format '{{index .Config.Labels "io.x-k8s.kind.cluster"}}' "$worker1" | grep -Fx "$cluster"
  docker stop --time 10 "$worker1" > "$out/worker-stop.txt"
  docker inspect --format '{{.State.Running}}' "$worker1" | grep -Fx false > "$out/worker-running.txt"
  k wait --for=condition=Ready=unknown "node/$worker1" --timeout=150s
  snapshot during-outage
  jq -e --arg node "$worker1" 'any(.[]; .name==$node and any(.conditions[]; .status!="True"))' "$out/during-outage-nodes.json"
  local lost
  lost=$(jq -er --arg node "$worker1" '.[] | select(.node==$node) | .uid' "$out/before-outage-pods.json")
  local lost_name
  lost_name=$(jq -er --arg uid "$lost" '.[] | select(.uid==$uid) | .name' "$out/before-outage-pods.json")
  k wait --for=jsonpath='{.metadata.deletionTimestamp}' "pod/$lost_name" --timeout=420s ||
    [[ -z "$(k get pod "$lost_name" --ignore-not-found -o name)" ]]
  # The default not-ready/unreachable tolerations last 300 seconds.
  k rollout status deployment/multinode-api --timeout=180s
  snapshot outage-replaced
  deployment > "$out/outage-replaced-deployment.json"
  jq -e --arg node "$worker2" --arg lost "$lost" 'map(select(.deleting | not)) | length==2 and all(.[]; .node==$node and .uid!=$lost and any(.conditions[]; .type=="Ready" and .status=="True"))' "$out/outage-replaced-pods.json"
  jq -e --slurpfile before "$out/before-outage-deployment.json" '.generation==$before[0].generation and .observed==.generation and .ready==2' "$out/outage-replaced-deployment.json"
  request > "$out/http-outage-replaced.txt"
  docker start "$worker1" > "$out/worker-start.txt"
  k wait --for=condition=Ready node --all --timeout=180s
  k rollout status deployment/multinode-api --timeout=90s
  snapshot outage-recovered
  request > "$out/http-outage-recovered.txt"
}
network() {
  k run blocked --image=curlimages/curl:8.16.0 --restart=Never --labels=client=blocked --command -- sleep 900
  k wait --for=condition=Ready pod/blocked --timeout=120s
  local address
  address=$(k get svc multinode-api -o jsonpath='{.spec.clusterIP}')
  probe blocked "$address" allow baseline
  k apply -f cluster/multi-node/default-deny.yaml
  probe smoke "$address" deny default-deny
  probe blocked "$address" deny blocked-deny
  k apply -f cluster/multi-node/allow-client.yaml
  probe smoke "$address" allow allowed-client
  probe blocked "$address" deny blocked-client
  request > "$out/dns-http.txt"
  k delete -f cluster/multi-node/default-deny.yaml -f cluster/multi-node/allow-client.yaml
  probe blocked "$address" allow restored
}
probe() {
  local client=$1 address=$2 expected=$3 name=$4 attempt result code
  for attempt in {1..20}; do
    code=0
    result=$(k exec "$client" -- sh -c 'code=0; curl -sS -o /dev/null -w "%{http_code}" --max-time 3 "$1" 2>/dev/null || code=$?; printf ":%s" "$code"' sh "http://$address/readyz") || code=$?
    if [[ "$code" == 0 ]] && { [[ "$expected" == allow && "$result" == 200:0 ]] || [[ "$expected" == deny && "$result" =~ ^000:(7|28)$ ]]; }; then
      jq -n --arg client "$client" --arg expected "$expected" --arg result "$result" '{client:$client,expected:$expected,result:$result}' > "$out/$name.json"
      return 0
    fi
    sleep 1
  done
  echo "Probe $name: expected $expected, last result $result" >&2
  return 1
}
case "$group" in
  outage) outage ;;
  network) network ;;
  bootstrap) ;;
  scheduler) scheduler ;;
  maintenance)
    k label node "$worker1" lab.example/worker=one
    maintenance
    ;;
  *) echo "Unknown experiment group: $group" >&2; exit 1 ;;
esac
