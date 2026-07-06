# Translator Implementation Recap

This implementation adds the Scenario Manager side of the translator handoff workflow described in `translator_comm_impl_spec.md`.

What was added:

- A transport-neutral communication boundary in `scenario-manager/internal/communication/communication.go` for translation request publishing and translator ready consumption.
- A shared broker-subject normalization helper in `scenario-manager/internal/subject/subject.go`, and the existing EDS subject sanitizer now delegates to it.
- Shared translator workflow config parsing in `scenario-manager/internal/translatorconfig/translatorconfig.go` for max attempts and unpublished-claim recovery timeout.
- Core translator handoff logic in `scenario-manager/internal/core/translator_handoff.go`:
  - claim exactly one supplied scenario id
  - publish only after the claim commits
  - mark publish confirmation only after transport confirmation
  - recover publish failures for the exact claimed attempt
  - handle translator ready messages semantically
- Core DB support in `scenario-manager/internal/coredb/scenario_status.go` for:
  - exact-row translation claims
  - publish confirmation markers
  - publish-failure recovery
  - ready-message idempotency and stale-attempt handling
  - poison ready handling for empty container images
  - unpublished-claim recovery
- Schema updates in `scenario-manager/internal/coredb/schema.go` and state updates in `scenario-manager/internal/coredb/scenario_status.go`:
  - default state changed from `Pending` to `Created`
  - added `created_at`
  - added `updated_at`
  - added `translation_attempts`
  - added `translation_request_published_at`
- A NATS/JetStream translator adapter in `scenario-manager/internal/nats/trans_com.go` that:
  - reuses the shared NATS connection
  - ensures the translator stream exists
  - publishes translation requests with JetStream `PubAck` confirmation
  - consumes wildcard ready subjects through one durable queue consumer
  - validates ready subjects and JSON strictly
  - ACKs permanent poison/stale outcomes and NAKs transient failures

What was intentionally not added:

- No wiring into `scenario-manager/internal/core/scenario_manager.go`
- No translator startup integration in `cmd/main.go`
- No scenario-selection loop
- No heartbeat protocol

Notes:

- Function comments were added throughout the new translator workflow files and the materially changed helpers so outside developers can follow ownership and failure behavior directly in code.
- Validation was partial because full `go test ./...` was blocked by unavailable external Go module downloads in the current environment.
