#!/usr/bin/env python3
"""Mock EDS publisher for Scenario Manager integration tests.

Overview:
    This module implements a small but realistic Experimental Design Service
    (EDS) mock that exercises the Scenario Manager intake path over NATS and
    JetStream.

Protocol flow:
    1. Connect to NATS.
    2. For each batch:
       a. Send an availability request to the Scenario Manager on the
          configured request/reply subject.
       b. Parse the reply and extract the target batch subject.
       c. Publish a scenario batch payload to JetStream on that subject.
    3. Flush the connection and optionally stay alive so operators can inspect
       logs and pod state.

Why this exists:
    - Replaces one-off shell/Job publishers with a reusable container image.
    - Encodes retry behavior and startup delays required in Kubernetes e2e.
    - Keeps payload generation deterministic and configurable via environment.

Environment variables:
    `NATS_URL`:
        NATS endpoint (default: `nats://nats:4222`).
    `AVAILABILITY_SUBJECT`:
        Request/reply subject used for EDS availability handshake
        (default: `cbse.eds.scenarios.available`).
    `PROJECT_NAME`:
        SimulationExperiment/project identifier used in payloads.
    `BATCH_ID_PREFIX`:
        Prefix used to build batch ids: `<prefix>-<index>`.
    `TOTAL_BATCHES`:
        Number of full batches to publish (default: `2`).
    `SCENARIOS_PER_BATCH`:
        Number of scenarios per batch (default: `2`).
    `CONNECT_RETRIES`:
        Max retries while establishing NATS connection.
    `REQUEST_RETRIES`:
        Max retries while sending availability request.
    `RETRY_DELAY_SECONDS`:
        Sleep duration between retry attempts.
    `REQUEST_TIMEOUT_SECONDS`:
        Per-request timeout for availability handshake.
    `STARTUP_DELAY_SECONDS`:
        Initial delay before any NATS activity, helpful while dependent pods
        are still starting.
    `EDS_KEEP_ALIVE`:
        If true, keep the process alive after publishing, waiting for SIGINT or
        SIGTERM. If false, exit after publishing completes.
"""

from __future__ import annotations

import asyncio
import json
import os
import signal
from dataclasses import dataclass
from typing import Any

from nats.aio.client import Client as NATS
from nats.errors import TimeoutError


@dataclass(frozen=True)
class Config:
    """Runtime configuration derived from environment variables.

    The dataclass keeps all knobs in one immutable object so asynchronous
    helpers can share a consistent snapshot of configuration.
    """

    nats_url: str
    availability_subject: str
    project_name: str
    batch_id_prefix: str
    total_batches: int
    scenarios_per_batch: int
    connect_retries: int
    request_retries: int
    retry_delay_seconds: float
    request_timeout_seconds: float
    startup_delay_seconds: float
    keep_alive: bool


def env_int(name: str, default: int) -> int:
    """Parse a positive integer environment variable with fallback behavior.

    Invalid, empty, or non-positive values fall back to `default`.
    """

    raw = os.getenv(name)
    if raw is None:
        return default
    try:
        value = int(raw)
    except ValueError:
        return default
    return value if value > 0 else default


def env_float(name: str, default: float) -> float:
    """Parse a positive float environment variable with fallback behavior.

    Invalid, empty, or non-positive values fall back to `default`.
    """

    raw = os.getenv(name)
    if raw is None:
        return default
    try:
        value = float(raw)
    except ValueError:
        return default
    return value if value > 0 else default


def env_bool(name: str, default: bool) -> bool:
    """Parse a boolean-like environment variable.

    Truthy values:
        `1`, `true`, `yes`, `on` (case-insensitive).
    """

    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def load_config() -> Config:
    """Build and return a validated configuration snapshot.

    The method intentionally applies strict defaults so the mock is
    immediately runnable in-cluster without requiring additional setup.
    """

    project = os.getenv("PROJECT_NAME", "sm-eds-e2e").strip() or "sm-eds-e2e"
    return Config(
        nats_url=os.getenv("NATS_URL", "nats://nats:4222"),
        availability_subject=os.getenv(
            "AVAILABILITY_SUBJECT", "cbse.eds.scenarios.available"
        ),
        project_name=project,
        batch_id_prefix=os.getenv("BATCH_ID_PREFIX", f"batch-{project}"),
        total_batches=env_int("TOTAL_BATCHES", 2),
        scenarios_per_batch=env_int("SCENARIOS_PER_BATCH", 2),
        connect_retries=env_int("CONNECT_RETRIES", 60),
        request_retries=env_int("REQUEST_RETRIES", 60),
        retry_delay_seconds=env_float("RETRY_DELAY_SECONDS", 2.0),
        request_timeout_seconds=env_float("REQUEST_TIMEOUT_SECONDS", 5.0),
        startup_delay_seconds=env_float("STARTUP_DELAY_SECONDS", 3.0),
        keep_alive=env_bool("EDS_KEEP_ALIVE", True),
    )


async def connect_with_retries(cfg: Config) -> NATS:
    """Connect to NATS using bounded retries.

    Raises:
        RuntimeError: When all connection attempts fail.
    """

    client = NATS()
    for attempt in range(1, cfg.connect_retries + 1):
        try:
            await client.connect(servers=[cfg.nats_url], connect_timeout=2)
            print(f"[EDS] Connected to NATS on attempt {attempt}: {cfg.nats_url}")
            return client
        except Exception as exc:  # noqa: BLE001
            if attempt == cfg.connect_retries:
                raise RuntimeError(
                    f"failed to connect to NATS after {cfg.connect_retries} attempts"
                ) from exc
            print(f"[EDS] NATS connect attempt {attempt} failed: {exc}")
            await asyncio.sleep(cfg.retry_delay_seconds)
    raise RuntimeError("unreachable: connect retry loop completed unexpectedly")


def build_batch_payload(
    batch_id: str,
    project: str,
    scenarios_per_batch: int,
    seed_base: int,
) -> dict[str, Any]:
    """Construct one scenario batch payload compatible with Scenario Manager.

    Payload schema:
        {
          "batch_id": "...",
          "project": "...",
          "scenarios": [
            {
              "priority": <int>,
              "number_of_reps": <int>,
              "recipe_info": {...},
              "confidence_metric": <float>
            }
          ]
        }

    The generated values are deterministic from input parameters to make test
    behavior reproducible across repeated runs.
    """

    scenarios: list[dict[str, Any]] = []
    for idx in range(1, scenarios_per_batch + 1):
        scenarios.append(
            {
                "priority": idx,
                "number_of_reps": 10 + idx,
                "recipe_info": {
                    "scenario": f"{project}-scenario-{seed_base + idx}",
                    "seed": seed_base + idx,
                },
                "confidence_metric": round(0.90 + (idx * 0.01), 3),
            }
        )

    return {"batch_id": batch_id, "project": project, "scenarios": scenarios}


async def resolve_batch_subject(nc: NATS, cfg: Config, batch_id: str) -> str:
    """Perform availability handshake and return SM-provided batch subject.

    The request/reply contract is:
        request subject: `cfg.availability_subject`
        request payload: {"batch_id","project","scenario_count"}
        expected response: {"status":"ready","batch_subject":"..."}

    Retries are applied for both timeouts and transient transport/protocol
    errors.
    """

    availability_payload = {
        "batch_id": batch_id,
        "project": cfg.project_name,
        "scenario_count": cfg.scenarios_per_batch,
    }
    payload = json.dumps(availability_payload).encode("utf-8")

    for attempt in range(1, cfg.request_retries + 1):
        try:
            msg = await nc.request(
                cfg.availability_subject,
                payload,
                timeout=cfg.request_timeout_seconds,
            )
            response = json.loads(msg.data.decode("utf-8")) if msg.data else {}
            status = response.get("status", "")
            reason = response.get("reason", "")
            batch_subject = response.get("batch_subject", "")

            if status and status != "ready":
                raise RuntimeError(f"scenario manager rejected availability: {reason or status}")
            if not batch_subject:
                raise RuntimeError("scenario manager response did not include batch_subject")

            print(
                f"[EDS] Availability accepted for batch={batch_id}; batch_subject={batch_subject}"
            )
            return batch_subject
        except TimeoutError as exc:
            if attempt == cfg.request_retries:
                raise RuntimeError("availability request timed out repeatedly") from exc
            print(
                f"[EDS] Availability request attempt {attempt} timed out; retrying in {cfg.retry_delay_seconds}s"
            )
            await asyncio.sleep(cfg.retry_delay_seconds)
        except Exception as exc:  # noqa: BLE001
            if attempt == cfg.request_retries:
                raise RuntimeError("failed to resolve batch subject") from exc
            print(
                f"[EDS] Availability request attempt {attempt} failed: {exc}; retrying in {cfg.retry_delay_seconds}s"
            )
            await asyncio.sleep(cfg.retry_delay_seconds)

    raise RuntimeError("unreachable: request retry loop completed unexpectedly")


async def publish_batches(nc: NATS, cfg: Config) -> None:
    """Publish all configured batches to JetStream.

    For each batch we resolve the target subject dynamically using the
    availability handshake. This mirrors the intended production handshake
    where SM can control per-project subject routing.
    """

    js = nc.jetstream()

    for batch_index in range(1, cfg.total_batches + 1):
        batch_id = f"{cfg.batch_id_prefix}-{batch_index}"
        batch_subject = await resolve_batch_subject(nc, cfg, batch_id)
        payload = build_batch_payload(
            batch_id=batch_id,
            project=cfg.project_name,
            scenarios_per_batch=cfg.scenarios_per_batch,
            seed_base=batch_index * 100,
        )
        ack = await js.publish(batch_subject, json.dumps(payload).encode("utf-8"))
        print(
            f"[EDS] Published batch={batch_id} scenarios={cfg.scenarios_per_batch} stream={ack.stream} seq={ack.seq}"
        )

    await nc.flush()
    print(
        f"[EDS] Finished publishing {cfg.total_batches} batches with {cfg.scenarios_per_batch} scenarios each."
    )


async def wait_forever() -> None:
    """Block until SIGINT/SIGTERM is received.

    This is used when `EDS_KEEP_ALIVE=true`, allowing log inspection and
    easier operational debugging in Kubernetes after publishes complete.
    """

    stop_event = asyncio.Event()
    loop = asyncio.get_running_loop()

    def stop() -> None:
        stop_event.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, stop)
        except NotImplementedError:
            # Signal handlers are unavailable on some platforms.
            pass

    await stop_event.wait()


async def async_main() -> None:
    """Entrypoint for async lifecycle: configure, publish, and shutdown."""

    cfg = load_config()
    if cfg.startup_delay_seconds > 0:
        print(f"[EDS] Startup delay: {cfg.startup_delay_seconds}s")
        await asyncio.sleep(cfg.startup_delay_seconds)

    nc = await connect_with_retries(cfg)
    try:
        await publish_batches(nc, cfg)
        if cfg.keep_alive:
            print("[EDS] Keep-alive enabled; waiting for termination signal.")
            await wait_forever()
    finally:
        await nc.drain()
        await nc.close()
        print("[EDS] Connection closed.")


def main() -> None:
    """Synchronous wrapper for process entrypoint."""

    asyncio.run(async_main())


if __name__ == "__main__":
    main()
