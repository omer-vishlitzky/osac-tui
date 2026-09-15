#!/usr/bin/env bash

set -euo pipefail

: "${KUBECONFIG:?set KUBECONFIG to the target cluster kubeconfig}"

namespace=${OSAC_NAMESPACE:-osac}
client_id=${OSAC_CLIENT_ID:-osac-admin}
api_host=$(kubectl -n "$namespace" get route fulfillment-api -o jsonpath='{.spec.host}')
keycloak_host=$(kubectl -n keycloak get route keycloak -o jsonpath='{.spec.host}')
client_secret=$(kubectl -n keycloak get secret keycloak-client-secrets -o jsonpath="{.data.${client_id}}" | base64 --decode)

ca_file=$(mktemp)
trap 'rm -f "$ca_file"' EXIT
kubectl -n "$namespace" get configmap ca-bundle -o jsonpath='{.data.bundle\.pem}' > "$ca_file"

issuer="https://${keycloak_host}/realms/osac"
token=$(curl --fail --silent --show-error --cacert "$ca_file" \
  --user "${client_id}:${client_secret}" \
  --data-urlencode grant_type=client_credentials \
  --data-urlencode "client_id=${client_id}" \
  "${issuer}/protocol/openid-connect/token" | jq --exit-status --raw-output '.access_token')

exec go run ./cmd/osac-tui \
  --address "${api_host}:443" \
  --tls \
  --ca-file "$ca_file" \
  --token "$token" \
  "$@"
