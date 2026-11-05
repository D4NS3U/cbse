# Copyright 2025-2026 Daniel Seufferth
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

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
