"""Pure local-PRNG seed derivation for the reference runner.

The runner derives a local PRNG seed from the baked seed policy and the runtime
hostname so the same hostname produces a stable seed and different hostnames
produce different seeds. The seed is a non-negative 63-bit integer suitable for
``random.Random``. This module has no third-party dependencies so it can be
unit-tested without simpy or psycopg.
"""

import hashlib


def derive_seed(seed_policy, hostname):
    """Return a stable 63-bit seed derived from seed_policy and hostname.

    Raises ``SystemExit`` on an empty hostname. The seed is the first eight
    bytes of sha256("<seed_policy>:<hostname>") interpreted big-endian and
    masked to 63 bits.
    """
    if not hostname:
        raise SystemExit("runtime hostname must not be empty")
    digest = hashlib.sha256("{}:{}".format(seed_policy, hostname).encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big") & 0x7FFF_FFFF_FFFF_FFFF
