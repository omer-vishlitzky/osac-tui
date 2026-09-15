# osac-tui

`osac-tui` is a terminal UI for the OSAC fulfillment-service API. It is intended
to feel like `k9s` for fulfillment resources: pick a resource kind, inspect
objects as YAML, and use the same screen to create, update, refresh, or delete
them.

The fulfillment service must already be running at the address passed to
`--address`. The TUI opens before the first RPC completes, so connection errors
appear in the status line instead of blocking startup.

## Run

The local fulfillment-service gRPC endpoint is plaintext by default:

```sh
go run ./cmd/osac-tui --address localhost:8000
```

For a TLS endpoint and bearer token:

```sh
go run ./cmd/osac-tui \
  --address fulfillment-api.example.com:443 \
  --tls \
  --token "$OSAC_TOKEN"
```

Use `--ca-file` when the service certificate is signed by a private CA.

## Keys

| Key | Action |
| --- | --- |
| `tab` | Choose a resource kind |
| `j`/`k`, arrows | Move through rows or resource kinds |
| `enter` | Open the selected object |
| `c` | Create an object from YAML |
| `e` | Edit the selected object as YAML |
| `d` | Delete the selected object |
| `r` | Refresh |
| `ctrl+s` | Submit YAML changes |
| `esc` | Go back or cancel |
| `q` | Quit |

The editor submits the complete protobuf object. Updates use the object's
`metadata.version` for optimistic locking.

## Scope

The client currently registers the public CRUD resources most useful when
operating infrastructure: `clusters`, `computeinstances`, `virtualnetworks`,
`subnets`, and `securitygroups`. The registry is deliberately typed so adding
another generated service is a small, compile-time checked adapter rather than
reflection-driven CRUD.
# osac-tui
