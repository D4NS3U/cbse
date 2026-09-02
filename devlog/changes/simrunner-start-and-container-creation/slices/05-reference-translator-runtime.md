# Slice 05: Reference Translator runtime

## Goal

Provide the reference Translator framework, replaceable generator, generated runner runtime, and durable image-result handoff.

## Dependencies

[Slice 03](03-operator-provisioning.md) supplies deployment and mounted configuration; [Slice 04](04-sm-messaging-and-lifecycle.md) supplies messaging and lifecycle contracts.

## Contracts consumed

This slice consumes the root [identity](../FEATURE.md#identity-and-lifecycle-invariants), [registry credential](../FEATURE.md#registry-credential-boundary), [state](../FEATURE.md#state-boundary), and [failure-classification](../FEATURE.md#configuration-validation-and-failure-classes) contracts.

## Files and components owned

4. `component-templates/translator/` and its tests -- add the specified static Go Translator Dockerfile and hashed runner-base Dockerfile and requirements lock; implement the Go reference framework, namespace-aware request/ready subjects, per-experiment consumer, replaceable SimPy generator, fixed Scenario Detail Database lookup and bounded retry, the Go DNS/IPv4/IPv6 modules used for that lookup, the equivalent generated Python modules used by the runner for Result DB connections, attempt workspaces, BuildKit client, authenticated push, durable pushed-outcome recovery, digest resolution, and ready-message acknowledgement ordering. Retire `test/mocks/translator` from the full smoke path.
5. `component-templates/scenario-detail-database/` -- add the reference Scenario Detail Database image, based on the locked `POSTGRES_IMAGE`, with checked-in `/docker-entrypoint-initdb.d/` SQL that creates `public.simulation_parameters`, grants the required schema/table access, and inserts the four immutable parameter rows. The smoke fixture selects this image but does not create or insert those rows.

## Required behavior

3. **Reference Translator template and on-demand image creation.**
   - Add a Go reference implementation at `component-templates/translator/`. It is the supported integration template for user-defined Translators and replaces `test/mocks/translator` in the full smoke path.
   - Separate the implementation into three explicit layers: the CBSE-maintained reference framework, a replaceable model-generator module, and the generated scenario-specific runner image. Production-quality guarantees apply to the framework and its integration contracts, not to the example model's semantics.
   - The Operator configures Translator through the same authentication-free environment-based process used by the existing smoke mock: `NATS_URL`, Translator stream, exact request subject, ready-subject template, and UID-specific durable-consumer name. `NATS_URL` contains no user information or credential material. No NATS credential environment variable, Secret, token, NKey, JWT, or credentials-file mount is added in alpha4. The Operator also injects `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID`, `REPOSITORY`, and `BASEIMAGE`. The template validates all values before connecting to NATS.
   - `BASEIMAGE` is sourced only from `spec.translator.baseimage`. EDS payloads, Scenario Status, and existing Translator request and ready payloads do not carry or persist base-image information.
   - The Operator mounts the existing `<experiment-name>-detaildb-sct` and `<experiment-name>-resultdb-sct` Secrets read-only at `/detaildb-connection` and `/resultdb-connection` respectively for Translator. Each Secret contains `host`, `port`, `dbname`, `user`, and `password`. Database credentials may be literal deployment configuration in this trusted prototype, but the generator does not duplicate them in source code: the framework reads the mounted files and passes typed connection values to the generator. This allowance does not apply to third-party credentials such as registry or robot-account credentials, Kubernetes kubeconfigs, or other externally managed service credentials.
   - SM is the only supported request publisher and marshals requests from the typed four-field request struct after validating positive scenario ID and translation attempt. The template performs the lightweight decoding defined below and handles one request at a time. Its workspace is `/workspace/scenario-<scenario-id>/attempt-<attempt>`. It checks for a retained outcome before removing or recreating build input; only an attempt with no retained outcome and no recoverable registry tag has its build directory recreated.
   - The template uses the official BuildKit Go client over `unix:///run/buildkit/buildkitd.sock`. It submits the generated attempt directory as both the Dockerfile and build context, supplies registry credentials from `/registry-auth/config.json` through the BuildKit session, and requests a registry push to the deterministic tag. Before creating or attaching its JetStream request consumer, Translator waits for that socket and a successful BuildKit `ListWorkers` call as defined by the sidecar readiness contract below.
   - The pushed tag is `<repository>:runner-<12-char-UID-prefix>-s<scenario-id>-a<attempt>`. The UID prefix is the first 12 lowercase hexadecimal characters of the UID after removing hyphens. The framework adds OCI manifest annotations for the full experiment UID, scenario ID, and translation attempt, verifies them after the push, resolves the tag, and publishes only the digest reference.
   - Translator publishes the ready message and waits for publish confirmation before acknowledging the request. A generator, BuildKit, push, or digest-resolution failure persists and publishes the existing ready message with an empty image and is acknowledged only after that publish succeeds. NATS transport or ready-publication failures are retryable and must not manufacture an empty result.
   - A successful push is a durable external effect. If ready publication then fails, Translator retains the resolved digest and retries publication of the same ready message. It must not regenerate, rebuild, or push another image for that scenario and attempt.
   - After a terminal acknowledged outcome, the template removes the attempt workspace. Before generation on redelivery, it validates any retained success or empty-failure outcome for that scenario and attempt and republishes that exact ready message without repeating the failed operation. A retained success reuses its digest without another registry lookup, generator call, build, or push. If a success marker was lost, Translator resolves the deterministic registry tag and reuses it only when its repository and framework-owned identity annotations exactly match the current experiment UID, scenario ID, and attempt. A missing or mismatched annotation is a permanent tag collision: Translator records and publishes an empty-image outcome, does not adopt or overwrite the tag, and does not invoke the generator or BuildKit. It recreates build input only when no marker or registry tag exists.

### Translator template boundary

The reference Translator is production-quality integration code, but it is not a universal production Translator. The stable framework owns request consumption and validation, serial delivery, workspace lifecycle, BuildKit sessions, registry authentication, push and digest verification, ready publication, and request acknowledgement. It must not contain model-specific decisions outside the example generator.

The Operator provides the framework configuration as environment variables, following the existing authentication-free mock Translator process. `NATS_URL` identifies the NATS server and must not contain user information. `TRANSLATOR_STREAM`, `TRANSLATOR_REQUEST_SUBJECT`, `TRANSLATOR_READY_SUBJECT_TEMPLATE`, and `TRANSLATOR_CONSUMER` identify the exact alpha4 JetStream stream, request filter, ready-subject template, and UID-specific durable consumer. `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, and `SIMULATIONEXPERIMENTUID` identify the owning experiment. `REPOSITORY` is the configured target repository and `BASEIMAGE` is the digest-pinned `spec.translator.baseimage` value. The template accepts no NATS username, password, token, NKey, JWT, credentials file, or credential mount. It fails startup with a descriptive configuration error when any required value is missing, malformed, or inconsistent with the alpha4 subject grammar. It creates neither shared stream nor Scenario Manager consumer.

After static startup validation, Translator applies the mandatory BuildKit admission gate before it creates or attaches its per-experiment request consumer. It checks `/run/buildkit/buildkitd.sock` and invokes BuildKit `ListWorkers`; a successful response with at least one worker opens the gate. Until then it retries at `250ms`, `500ms`, `1s`, and then every `2s`, honoring shutdown cancellation. A Pod may therefore be Running while it is not a request consumer, but a request cannot be accepted and classified as an empty-image outcome solely because the sidecar is still starting. The BuildKit sidecar startup probe is the Kubernetes-level counterpart of this gate. If BuildKit becomes unavailable only after Translator accepted a request, that request follows the normal BuildKit failure and confirmed empty-image workflow.

The framework derives the ready subject from the configured template and the validated request scenario ID. It must publish only within its injected namespace and project. The stream, request subject, ready-subject template, and consumer naming are configuration, not model-generator responsibilities.

Translator consumes the request schema already published by SM: integer `id`, integer `translation_attempt`, object-or-null `recipe_info`, and number-or-null `confidence_metric`. SM validates the two positive identity values before using `encoding/json` on that typed struct; no valid SM-published scenario request can therefore lack recoverable identity. Translator decodes one JSON object, requires positive `id` and `translation_attempt`, and checks the documented types for the two optional fields. Unknown fields are ignored and do not extend the supported contract. A delivery that cannot recover valid identity is external or corrupt poison rather than a request produced by the supported SM publisher: Translator emits one sanitized log and ACKs it without a ready message, and tests for this branch publish raw poison without creating a Core DB scenario. If identity is valid but `recipe_info` cannot be handled by the selected generator, that is a generator failure and follows the confirmed empty-image ready workflow for that scenario.

An empty-image outcome is terminal only after its ready publication is confirmed. Generator, BuildKit, registry push, and digest-resolution failures first atomically persist an empty-failure outcome marker, then retry publication of that same empty-image ready message until confirmation and finally server-ACK the request. If final request acknowledgement fails, redelivery reuses the marker and does not repeat generation, build, push, or resolution. NATS transport, progress-acknowledgement, or ready-publication failures leave the request unacknowledged for JetStream redelivery and never create a second failure classification. This is the same ACK/NAK boundary used by SM for ready messages: malformed or permanent poison is ACKed, while transient transport or persistence failure is retried.

For an active `InProgress` experiment admitted by the lifecycle gate, SM handles a confirmed empty-image ready message through the existing exact-attempt policy, now made canonical here. `SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` is a positive integer with default `3`. After validating project, scenario ID, state `Scheduled`, and matching `translation_attempt`, SM calls `MarkScenarioTranslationAttemptFailed`: attempts below the configured maximum transition that exact row back to `Created`, while an exhausted attempt transitions it to `Failed`. The recovery update does not increment `translation_attempts`; the next successful `Created -> Scheduled` claim increments it. SM ACKs the empty ready only after the guarded database update succeeds and NAKs transient database failures. For an inactive terminal or deleting experiment, SM instead applies the terminal ACK-and-discard rule and does not call this recovery update.

The replaceable Go generator boundary has this conceptual shape; the implementation may place these types in an internal package, but it must preserve this dependency direction:

```go
type Generator interface {
	Generate(context.Context, GenerationInput) error
}

type GenerationInput struct {
	ScenarioID         int
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
	BaseImage           string
	Workspace           string
	DetailDatabase      DatabaseConfig
	ResultDatabase      DatabaseConfig
}

type DatabaseConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
}
```

`Generate` writes a complete Docker build context into `Workspace`: generated model files, scenario configuration, dependencies, an entrypoint, and a Dockerfile. It does not consume NATS messages, call BuildKit, push images, publish ready messages, or acknowledge deliveries. Those remain framework responsibilities so a user can replace model generation without reimplementing the handoff protocol.

The same endpoint-resolution contract applies to exactly three PostgreSQL clients in this feature: the Operator availability probe for both configured databases, the Go Translator lookup client for the Scenario Detail DB, and the generated Python runner client for the Result DB. Their purposes remain distinct. The Operator does not read model parameters, create application tables, write results, or otherwise use a database for experiment work; after it has reconciled an image-based database or recorded an external endpoint, it performs only the provisioning sanity check defined below. Translator performs only the predefined Detail DB parameter query. The runner performs only its Result DB schema-and-insert workflow.

Each client classifies and normalizes the configured host exactly as defined by alpha4 admission. At the start of every new connection attempt, an IPv4 or IPv6 literal produces one normalized address without DNS. A DNS endpoint is resolved anew during that attempt through the client's context-aware system resolver for both A and AAAA records. Results retain resolver order, duplicate normalized addresses are removed by retaining their first occurrence, and an empty result is a resolution failure. The client tries addresses in that order; the first successful PostgreSQL connection owns the operation, and remaining addresses are not contacted. DNS resolution and all candidate connection attempts share one 10-second deadline rather than receiving 10 seconds each. An established connection is never re-resolved or migrated when DNS changes; a later connection attempt performs a fresh lookup. DNS failure, an empty result, or exhaustion of every returned address is a transient connection failure under the owning client's failure policy. Every implementation supplies host and port as separate driver fields and explicitly sets `sslmode=disable`; it must not construct a connection URI through string concatenation.

The Operator implements this contract in its internal database-endpoint package. The Translator template ships independently testable Go modules at `internal/databaseendpoint/dns`, `internal/databaseendpoint/ipv4`, and `internal/databaseendpoint/ipv6`, selected by one dispatcher. The generator also copies equivalent Python modules into every generated runner at `runner/database_endpoint/dns.py`, `runner/database_endpoint/ipv4.py`, and `runner/database_endpoint/ipv6.py`; the Python dispatcher uses `socket.getaddrinfo(..., type=socket.SOCK_STREAM)` and applies the same normalization, stable de-duplication, ordering, shared-deadline, and fallback rules before giving the selected address and separate port to Psycopg. These modules contain no credentials and are shipped code, not separately deployed services.

The intended example remains a small parameterizable SimPy model, but its parameters are stored in the PostgreSQL Scenario Detail Database rather than carried completely in the Translator request. SM continues to send the unchanged `recipe_info` field. For this generator, `recipe_info` must be exactly one JSON object containing one positive integer lookup key and no other fields:

```json
{"parameterset_id": 1}
```

This lookup `parameterset_id` identifies a row in the Scenario Detail Database. It is independent of the positive Core DB scenario ID in the request's top-level `id` field; the generator must not require those two values to be equal and this feature makes no Core DB schema or payload change for the lookup rename. A missing or null `recipe_info`, malformed JSON, a non-object value, a missing, non-integer, zero, or negative `parameterset_id`, or any unknown field is a permanent generator-input failure.

The reference Scenario Detail Database image owns this exact schema-qualified table:

```sql
CREATE TABLE public.simulation_parameters (
    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),
    arrival_rate    INTEGER NOT NULL CHECK (arrival_rate > 0),
    service_rate    INTEGER NOT NULL CHECK (service_rate > 0),
    run_duration    INTEGER NOT NULL CHECK (run_duration > 0),
    seed_policy     INTEGER NOT NULL CHECK (seed_policy >= 0)
);

GRANT USAGE ON SCHEMA public TO CURRENT_USER;
GRANT SELECT ON TABLE public.simulation_parameters TO CURRENT_USER;
```

All five values are PostgreSQL `INTEGER`s. `arrival_rate` is the number of arrivals per SimPy time unit, `service_rate` is the number of services per SimPy time unit, `run_duration` is the number of SimPy time units to execute, and `seed_policy` is non-negative seed material from which each runner derives its own pseudorandom seed. The repository-owned image initialization SQL creates the table and supplies exactly four rows. Their randomly selected values are generated once and checked in as fixed, reproducible image inputs rather than regenerated during image build, container startup, or a smoke run:

```sql
INSERT INTO public.simulation_parameters
    (parameterset_id, arrival_rate, service_rate, run_duration, seed_policy)
VALUES
    (1, 2, 4, 100, 1001),
    (2, 3, 5, 120, 1002),
    (3, 4, 7, 150, 1003),
    (4, 5, 8, 180, 1004);
```

The generator, not `recipe_info`, owns the one predefined parameterized SQL statement:

```sql
SELECT arrival_rate, service_rate, run_duration, seed_policy
FROM public.simulation_parameters
WHERE parameterset_id = $1;
```

The generator passes the validated `recipe_info.parameterset_id` as `$1`; it never executes SQL text received in a message. The query must return exactly one row. No row, more than one row, a `NULL`, a non-integer value, or a value outside the table constraints is a permanent generation failure. The primary key makes duplicate rows invalid schema state, but the adapter still checks cardinality defensively rather than silently selecting one row. All implementation SQL names this table as `public.simulation_parameters`; it must not depend on PostgreSQL `search_path`.

The framework reads the existing `<experiment-name>-detaildb-sct` Secret from `/detaildb-connection`, validates exactly the `host`, `port`, `dbname`, `user`, and `password` files at startup, and passes the resulting `DatabaseConfig` to the generator. This is identical whether the alpha4 `DatabaseSpec` selected an Operator-deployed image or an external `host`; the Operator creates the connection Secret in both cases. There is no additional Translator-specific Detail DB role. `spec.detailDatabase.user` is the exact PostgreSQL login used by Translator, and the Secret's `user` value must equal it. For the reference image form, the Operator also supplies `POSTGRES_USER` from that field, `POSTGRES_PASSWORD` from `spec.detailDatabase.password`, and `POSTGRES_DB` from `spec.detailDatabase.dbname`; therefore, image initialization and Translator use the same `POSTGRES_USER` account. The initialization SQL runs as that account, which owns the reference table under the official PostgreSQL image. Its explicit `GRANT USAGE ... TO CURRENT_USER` and `GRANT SELECT ... TO CURRENT_USER` statements document required access but do not reduce its owner privileges. For an external host, CBSE creates no role: the deployment owner must provision the configured account with at least `CONNECT`, `USAGE` on schema `public`, and `SELECT` on `public.simulation_parameters`; additional privileges are permitted. Database credentials may be literal CBSE deployment configuration, but the generator source contains no duplicated credential constants and the Scenario Detail DB values are not baked into the generated runner image. The reference image initialization owns creation and population of its four rows. Detail DB rows are immutable for this feature: no CBSE component updates or deletes them, and parameter snapshots, versions, or reload-after-translation behavior are not defined.

Each lookup attempt uses a 10-second connection timeout and a 30-second statement timeout. Connection refusal, either timeout, a PostgreSQL connection exception (`08`), transaction rollback (`40`), insufficient-resource error (`53`), or administrative shutdown/crash/cannot-connect-now result (`57P01`, `57P02`, or `57P03`) is transient. After the first transient failure, the generator keeps the request active, continues the framework's 30-second JetStream in-progress acknowledgements, waits exactly 30 seconds with context cancellation, reconnects, and executes the same query once more. Success on the second attempt continues generation. A second transient failure becomes a permanent generation failure. Authentication failures (`28`), insufficient privilege (`42501`), missing relation or other schema errors, invalid recipe input, and invalid query results fail immediately without the delayed retry. Shutdown or failed JetStream in-progress acknowledgement cancels the lookup or wait and leaves the request unacknowledged; it does not create an empty-failure outcome.

Once a lookup has permanently failed, the framework treats it like another generator failure: it atomically persists the empty-failure outcome, publishes the existing empty-image ready message with confirmation, and acknowledges the request only through the normal confirmed-outcome workflow. A successful lookup supplies the four validated integers to the example generator, which packages the checked-in SimPy model and writes the parameterized model into the generated build context. The generated Dockerfile uses the required digest-pinned runner smoke base image, copies only the generated runner files and required Result DB runtime connection configuration, sets a numeric non-root user, and defines the model launcher as its entrypoint. It does not reinstall unversioned dependencies. The generated image starts the baked scenario without command, arguments, or environment variables from SM; it obtains its Pod hostname from the container runtime with Python's `socket.gethostname()`.

The example is a single-server queue implemented with one `simpy.Resource(capacity=1)`. A source process samples interarrival times with `rng.expovariate(arrival_rate)` and creates customers only while their sampled arrival time is less than `run_duration`. Each customer records the time spent waiting for the resource, samples its service time with `rng.expovariate(service_rate)`, and increments the completed count only if service finishes by the simulation horizon. The runner executes `env.run(until=run_duration)`; unfinished customers are neither completed nor included in the mean. `mean_wait_time` is the arithmetic mean for completed customers and is `0.0` when no customer completes.

Each runner container constructs a local Python `random.Random` instance. It requires a non-empty value from `socket.gethostname()` and applies this exact derivation; it must not use module-global random state:

```python
hostname = socket.gethostname()
digest = hashlib.sha256(f"{seed_policy}:{hostname}".encode("utf-8")).digest()
effective_seed = int.from_bytes(digest[:8], "big") & 0x7FFF_FFFF_FFFF_FFFF
rng = random.Random(effective_seed)
```

Kubernetes uses the Pod name as each container's runtime hostname, and replacement Pods receive new names, so repetitions and retries receive independently derived seeds without changing the Core DB, Translator payload, or Job completion-index contract. The derivation is stable for the same `seed_policy` and Pod name and provides practical collision resistance, not a formal uniqueness proof.

The example runner writes one JSONB result with `parameterset_id`, `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`, `effective_seed`, `completed_customers`, and `mean_wait_time`. The first five fields reproduce the lookup input and parameters; `effective_seed` is the derived non-negative 63-bit integer, `completed_customers` is a non-negative integer, and `mean_wait_time` is a non-negative JSON number. The Result DB table remains named from the top-level Core DB scenario ID as `scenario_<scenario-id>_results`; `parameterset_id` does not replace that Core DB identity.

At startup, the template reads exactly `host`, `port`, `dbname`, `user`, and `password` from both `/detaildb-connection` and `/resultdb-connection` and reads the registry configuration mounted at `/registry-auth/config.json`. It rejects missing or malformed values before handling requests, requires basic credentials for the configured base-image and target-repository authorities through the same Docker resolver used by the Operator, and passes the two validated `DatabaseConfig` values only to the generator. Translator opens neither database at startup. The template does not watch, reload, or rotate registry or database credentials while running. Secret updates after startup have no defined effect until the Translator Pod restarts.

The Operator's database connection is an availability-only provisioning probe. After the Deployment and Service for an image form, or the connection Secret for a host form, exist, the Operator creates a fresh client for each Detail and Result database using the common endpoint contract, connects within the shared 10-second resolution-and-connection deadline, executes exactly `SELECT 1` under a 30-second statement timeout, closes the client, and retains no pool or session. It never performs Detail DB parameter lookup, Result DB table creation or inserts, schema validation, DDL, or data mutation through this probe. An invalid connection field is a provisioning error. A syntactically valid endpoint that cannot resolve or connect, times out, is temporarily unavailable, or rejects authentication leaves the experiment in `Provisioning` and requeues the complete probe after five seconds without a fixed attempt limit. Only a successful probe of both databases permits `InProgress`; deletion cancels an active probe or wait. Database transport security and related API fields remain outside this feature, and all three clients explicitly use `sslmode=disable`.

The example generator bakes the Result DB connection configuration and a replaceable result-sink interface into the runner image; it does not bake the Scenario Detail DB configuration because parameter lookup finishes before the image build. This retains the trusted prototype assumption that the generated image contains Result DB credentials: neither the framework, generator, BuildKit progress handling, nor runner may print those values or expose them in image labels or annotations. Database credentials must not be added to NATS messages, Scenario Status, image labels, or annotations.

The configured Result DB role must have `CONNECT` on the configured database, `USAGE` and `CREATE` on its default schema, and permission to create and write the tables it owns; no superuser privilege is required. The example runner uses a 10-second connection timeout and a 30-second statement timeout and performs no inner retry. It connects to PostgreSQL and, in a short transaction, obtains `pg_advisory_xact_lock(0, <scenario-id>)` before creating the scenario table with `IF NOT EXISTS`; the transaction commits immediately after the schema check. This serializes only concurrent first-use table creation by repetitions of the same scenario and prevents catalog races. The runner then performs its result insert in a separate transaction. A timeout or permission failure exits non-zero and consumes the current Kubernetes Pod attempt. The table name and advisory-lock key are derived only from the already validated positive integer scenario ID:

```sql
CREATE TABLE IF NOT EXISTS scenario_<scenario-id>_results (
    id BIGSERIAL PRIMARY KEY,
    result JSONB NOT NULL
);
```

After a successful simulation, each runner appends the JSONB result object defined above. The sequence supplies unique row IDs but does not claim commit-order semantics. Completion indexes are not written to the Result DB, and results have no repetition identifier or uniqueness constraint. A simulation failure performs no result insert. A Result DB connection, advisory-lock, table-creation, or insert failure makes the runner Pod fail and consumes the shared Job retry budget. Kubernetes may create a replacement Pod while that global budget remains.

The result sink is intentionally at-least-once. If PostgreSQL commits an insert but the runner loses the response, the runner exits unsuccessfully and a replacement Pod may run the simulation and append another row. This trusted prototype accepts that an ambiguous commit can therefore produce more result rows than successful completion indexes. The normal no-failure smoke path still requires exactly `number_of_reps` rows. A user-defined generator may replace the per-scenario schema and sink independently, but it must preserve the generated image's ownership of runner startup and result handling.

The namespace-aware subjects above replace the project-only subject names; the existing request and ready JSON payloads remain unchanged. The template uses the exact JetStream consumer contract above and allows at most one active request and build. A successful digest ready message, or a confirmed empty-image failure ready message, is published with JetStream confirmation before the request receives its server-confirmed acknowledgement. NATS transport or publication failures leave the request unacknowledged and retryable; they do not produce an empty-image result.

The BuildKit image exporter adds these credential-free OCI manifest annotations:

- `experiment.cbse.terministic.de/experiment-uid=<full-UID>`
- `experiment.cbse.terministic.de/scenario-id=<decimal-scenario-id>`
- `experiment.cbse.terministic.de/translation-attempt=<decimal-attempt>`

`ready-outcome.json` is the single credential-free durable outcome format. It contains `outcome` (`success` or `empty_failure`), scenario ID, translation attempt, ready subject, and full experiment UID. A success additionally contains deterministic tag and digest; an empty failure contains an empty image and a short non-sensitive failure class. Translator writes the complete file to a temporary file in the same directory and renames it over the marker before publishing ready. It retains the workspace until ready publication and server-confirmed request acknowledgement both succeed.

After BuildKit confirms a successful push, Translator resolves the digest and verifies the repository and all three annotations, then atomically writes the success outcome. A generator, BuildKit, push, digest-resolution, or tag-collision failure atomically writes the empty-failure outcome before its first ready publication. A publish or final-ack retry validates and reuses the exact marker without another generator, build, push, or registry lookup.

On redelivery or process restart, Translator first validates the marker's outcome, experiment UID, scenario ID, attempt, ready subject, and outcome-specific fields. A valid marker is authoritative for that attempt. If the marker was lost because the Pod was replaced, Translator resolves the deterministic registry tag. It treats the tag as the successful build outcome only when the normalized repository and all three manifest annotations exactly match the current request. A missing or mismatched annotation is a permanent identity collision that creates the empty-failure marker; Translator never adopts or overwrites the tag. A registry not-found result permits generation and build; another resolution failure creates the empty-failure marker.

An empty-failure marker is durable across Translator process or container restart while the Pod-local `emptyDir` survives, but there is deliberately no external empty-failure artifact. If Pod replacement loses that marker, redelivery may repeat generation or another failing operation once; SM's exact-attempt handling makes any resulting ready message stale after the first empty outcome was applied. This bounded duplicate work is accepted and must not change a newer scenario attempt. Successful pushes continue to recover externally from their annotated registry tag.

## Failure and retry behavior

The retained outcome marker, registry-tag recovery, bounded Detail DB retry, progress acknowledgements, confirmed ready publication, and final request acknowledgement define the retry boundary.

## Security boundaries

The framework receives registry credentials only through the mounted Docker configuration, keeps database credentials out of messages and annotations, runs non-root, and preserves the trusted-prototype limitations documented in the root.

## Acceptance tests

Test BuildKit readiness independently. Require the sidecar command to bind only `unix:///run/buildkit/buildkitd.sock` and retain `--oci-worker-no-process-sandbox`; require the exact `buildctl --addr unix:///run/buildkit/buildkitd.sock debug workers` startup probe, one-second period, and failure threshold `60`. With a missing socket, a socket that rejects calls, or zero workers, prove that Translator creates or attaches no request consumer, accepts no request delivery, writes no outcome marker, and consumes no translation attempt. Verify retries at `250ms`, `500ms`, `1s`, then `2s` until `ListWorkers` succeeds or shutdown cancels the wait. After the gate opens, force a BuildKit failure during an accepted request and require the ordinary confirmed empty-image outcome, proving the startup gate does not suppress real request-time failure handling.
Unit-test the template's runtime configuration, exact durable-consumer configuration, typed SM request publication, lightweight decoding of the four established request fields, generator invocation boundary, serial processing, deterministic workspace and tag construction, BuildKit calls, basic Docker-config authentication, digest and OCI identity-annotation validation, 30-second in-progress acknowledgements, confirmed ready publication, and server-confirmed request acknowledgement ordering. Raw poison without usable identity is ACKed without creating a DB scenario; generator failures with usable identity atomically persist and publish one empty-image outcome. Force ready publication and final request acknowledgement failures for both success and empty outcomes and verify redelivery or same-Pod restart reuses the exact marker with zero repeated generator, build, push, or registry calls. Test SM's default-three and configured translation-attempt limit, guarded `Scheduled -> Created` retry, and exhausted `Scheduled -> Failed`. Reject an incompatible existing durable without modifying it. If an in-progress acknowledgement fails, verify cancellation and redelivery. After success-marker loss, verify that a tag with matching repository and annotations is reused and that a missing, mismatched, or unannotated tag creates an empty outcome without rebuilding or overwriting it. After empty-marker loss through Pod replacement, permit one repeated operation and verify its ready message is stale and cannot change a newer attempt. Cover each currently defined startup setting and mounted path and verify Secret changes are not reloaded. Test the exact `public.simulation_parameters` schema, grants, and four reference-image rows; the exact `recipe_info` object and rejection of missing, null, malformed, non-positive, non-integer, or additional fields; independence of lookup `parameterset_id` from the top-level request `id`; schema-qualified parameterized SQL with no message-supplied SQL or `search_path` dependency; zero-, one-, and defensive multiple-row results; integer and constraint validation; exact use of the configured `POSTGRES_USER` account with no extra Translator Detail DB role; 10-second connection and 30-second statement timeouts; one cancellable 30-second delayed retry and no third attempt; immediate authentication, authorization, schema, recipe, and result failures; retry of the defined transient SQLSTATE classes; continued in-progress acknowledgements during lookup and delay; and cancellation without an empty outcome on shutdown or progress-ack failure. Unit-test the endpoint dispatcher and each DNS, IPv4, and IPv6 module independently, including mixed A/AAAA DNS results, stable de-duplication, resolver order, DNS failure, empty results, address exhaustion within one deadline, IPv4 normalization, unbracketed IPv6 normalization, and rejection of zones and ambiguous host/port strings. Verify that a permanent lookup failure follows the confirmed empty-image workflow and that success parameterizes the generated SimPy model and defined JSONB result without baking Detail DB credentials into the runner. Unit-test the exact queue horizon and no-completion behavior, local PRNG use, rejection of an empty runtime hostname, stable seed derivation for one hostname, different effective seeds for different hostnames, and all result fields. Verify Operator provisioning's five-second readiness retry and 10-second connection and 30-second statement timeouts for both databases, acceptance of the reference Detail DB owner account, Result DB operation without superuser privilege, exact-one-of image/host validation, and creation of correctly keyed connection Secrets for both image- and host-based Detail and Result databases without a Deployment or Service for host-based forms. Verify Result DB advisory-lock serialization of concurrent first-use table creation, concurrent inserts, controlled failure with no row, and a possible duplicate after ambiguous commit. Verify shared workspace access as UID/GID `1000` with `fsGroup: 1000`.

Run one table-driven endpoint conformance suite against the Operator Go probe adapter, Translator Go dispatcher, and generated Python runner dispatcher. Give all three the same literal and fake-resolver inputs and require identical classification, normalized addresses, stable A/AAAA ordering and de-duplication, one shared deadline, fallback choice, fresh DNS resolution on the next connection attempt, no re-resolution of an established connection, and separate host/port driver values. The owning client then applies its distinct behavior: Operator executes only `SELECT 1`, Translator executes only the schema-qualified parameter lookup, and runner executes only the Result DB workflow.

## Test tier

Implementation changes in this slice require `make test-fast` and `make test-smoke` because they change a container image and Kubernetes, NATS, registry, and database integration. Translator unit tests run with the race detector.

## Out of scope

A universal production generator, concurrent builds, untrusted-build isolation, credential rotation, and production image garbage collection remain excluded by the root [Out of Scope](../FEATURE.md#out-of-scope).

## Completion and handoff

The framework consumes one request at a time, generates and pushes an annotated immutable runner image, recovers durable outcomes without repeated work, and publishes ready before acknowledging. The reference Detail and Result database paths pass their defined success and failure tests.
