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

"""IPv4 address module for the common database endpoint contract.

A literal IPv4 host is normalized through ipaddress to its canonical form and
yields exactly one candidate address with no DNS resolution.
"""

import ipaddress


def normalize(host):
    """Validate that host is a literal IPv4 address and return its canonical
    string form. Bracketed, zone-scoped, or non-IPv4 values raise ValueError.
    """
    if host is None:
        raise ValueError("ipv4 host is required")
    trimmed = host.strip()
    if trimmed == "":
        raise ValueError("ipv4 host is required")
    if trimmed.startswith("[") or trimmed.endswith("]"):
        raise ValueError("ipv4 host {!r} must be an unbracketed address".format(trimmed))
    if "%" in trimmed:
        raise ValueError("ipv4 host {!r} must not contain a zone identifier".format(trimmed))
    try:
        addr = ipaddress.ip_address(trimmed)
    except ValueError as exc:
        raise ValueError("ipv4 host {!r} is not a literal IPv4 address: {}".format(trimmed, exc))
    if not isinstance(addr, ipaddress.IPv4Address):
        raise ValueError("ipv4 host {!r} is not an IPv4 address".format(trimmed))
    return str(addr)


def addresses(host):
    """Return the single normalized candidate address for a literal IPv4 host."""
    return [normalize(host)]
