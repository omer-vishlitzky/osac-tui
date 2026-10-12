#!/usr/bin/env bash

set -euo pipefail

: "${KUBECONFIG:?set KUBECONFIG to the target cluster kubeconfig}"

namespace=${OSAC_NAMESPACE:-osac}
service_account=${OSAC_SERVICE_ACCOUNT:-admin}
gateway_port=${OSAC_GATEWAY_PORT:-443}
private_api=${OSAC_PRIVATE_API:-true}
for argument in "$@"; do
  case "$argument" in
    --private-api=false|-private-api=false) private_api=false ;;
    --private-api=true|-private-api=true|--private-api|-private-api) private_api=true ;;
  esac
done
case "$private_api" in
  true) api_route=fulfillment-internal-api ;;
  false) api_route=fulfillment-api ;;
  *)
    printf 'run-cluster.sh: OSAC_PRIVATE_API must be true or false\n' >&2
    exit 1
    ;;
esac
api_address=${OSAC_ADDRESS:-}
if [[ -z "$api_address" ]]; then
  api_host=$(kubectl -n "$namespace" get route "$api_route" -o jsonpath='{.spec.host}')
  api_address="${api_host}:${gateway_port}"
fi
token=$(kubectl -n "$namespace" create token "$service_account")

ca_file=$(mktemp)
trap 'rm -f "$ca_file"' EXIT
kubectl -n "$namespace" get configmap ca-bundle -o jsonpath='{.data.bundle\.pem}' > "$ca_file"

exec go run ./cmd/osac-tui \
  --address "${api_address}" \
  --private-api="${private_api}" \
  --tls \
  --ca-file "$ca_file" \
  --token "$token" \
  "$@"
