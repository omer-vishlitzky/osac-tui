#!/usr/bin/env bash

set -euo pipefail

: "${KUBECONFIG:?set KUBECONFIG to the target cluster kubeconfig}"

namespace=${OSAC_NAMESPACE:-osac}
service_account=${OSAC_SERVICE_ACCOUNT:-admin}
gateway_port=${OSAC_GATEWAY_PORT:-443}
api_host=$(kubectl -n "$namespace" get route fulfillment-api -o jsonpath='{.spec.host}')
token=$(kubectl -n "$namespace" create token "$service_account")

ca_file=$(mktemp)
trap 'rm -f "$ca_file"' EXIT
kubectl -n "$namespace" get configmap ca-bundle -o jsonpath='{.data.bundle\.pem}' > "$ca_file"

exec go run ./cmd/osac-tui \
  --address "${api_host}:${gateway_port}" \
  --tls \
  --ca-file "$ca_file" \
  --token "$token" \
  "$@"
