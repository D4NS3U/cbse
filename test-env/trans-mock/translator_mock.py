#!/usr/bin/env python3
"""Deterministic Translator mock for the Scenario Manager handoff protocol."""

from __future__ import annotations

import asyncio
import json
import os
import signal

from nats.aio.client import Client as NATS


REQUEST_SUBJECT = os.getenv("TRANSLATOR_REQUEST_SUBJECT", "cbse.*.trans.request")
READY_SUBJECT = os.getenv("TRANSLATOR_READY_SUBJECT", "cbse.{project}.trans.{scenario_id}.ready")
STREAM = os.getenv("TRANSLATOR_STREAM", "cbse_translator")
CONSUMER = os.getenv("TRANSLATOR_CONSUMER", "translator-mock")
NATS_URL = os.getenv("NATS_URL", "nats://sm-eds-nats:4222")
DELAY_SECONDS = float(os.getenv("TRANSLATOR_DELAY_SECONDS", "20"))
IMAGE_PREFIX = os.getenv("TRANSLATOR_IMAGE_PREFIX", "trans.test")


def ready_subject(request_subject: str, scenario_id: int) -> str:
    parts = request_subject.split(".")
    if len(parts) != 4 or parts[0] != "cbse" or parts[2:] != ["trans", "request"]:
        raise ValueError(f"unexpected translator request subject: {request_subject}")
    return READY_SUBJECT.format(project=parts[1], scenario_id=scenario_id)


async def connect(nc: NATS) -> None:
    for attempt in range(1, 61):
        try:
            await nc.connect(servers=[NATS_URL], connect_timeout=2)
            print(f"[TRANS] connected to NATS on attempt {attempt}: {NATS_URL}", flush=True)
            return
        except Exception as exc:  # noqa: BLE001
            if attempt == 60:
                raise
            print(f"[TRANS] connection attempt {attempt} failed: {exc}", flush=True)
            await asyncio.sleep(2)


async def run() -> None:
    if DELAY_SECONDS < 0:
        raise ValueError("TRANSLATOR_DELAY_SECONDS must not be negative")
    nc = NATS()
    await connect(nc)
    js = nc.jetstream()
    sequence = 0
    stop = asyncio.Event()

    async def handle(msg) -> None:
        nonlocal sequence
        try:
            payload = json.loads(msg.data.decode("utf-8"))
            scenario_id = int(payload["id"])
            attempt = int(payload["translation_attempt"])
            if scenario_id <= 0 or attempt <= 0:
                raise ValueError("id and translation_attempt must be positive")
            subject = ready_subject(msg.subject, scenario_id)
        except (ValueError, KeyError, TypeError, json.JSONDecodeError) as exc:
            print(f"[TRANS] acknowledging malformed request {msg.subject}: {exc}", flush=True)
            await msg.ack()
            return

        print(f"[TRANS] translating scenario={scenario_id} attempt={attempt}; delay={DELAY_SECONDS}s", flush=True)
        await asyncio.sleep(DELAY_SECONDS)
        sequence += 1
        image = f"{IMAGE_PREFIX}:{sequence}"
        await js.publish(
            subject,
            json.dumps(
                {"translation_attempt": attempt, "container_image": image},
                separators=(",", ":"),
            ).encode("utf-8"),
        )
        await msg.ack()
        print(f"[TRANS] ready scenario={scenario_id} attempt={attempt} image={image}", flush=True)

    sub = await js.subscribe(
        REQUEST_SUBJECT,
        durable=CONSUMER,
        stream=STREAM,
        cb=handle,
    )
    print(f"[TRANS] listening request={REQUEST_SUBJECT} stream={STREAM} consumer={CONSUMER}", flush=True)
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    await stop.wait()
    await sub.unsubscribe()
    await nc.drain()


if __name__ == "__main__":
    asyncio.run(run())
