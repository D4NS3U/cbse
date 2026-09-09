"""IPv6 address module for the common database endpoint contract.

A literal IPv6 host is normalized through ipaddress to its canonical form and
yields exactly one candidate address with no DNS resolution. Bracketed and
zone-scoped forms are rejected so an established connection cannot be
re-resolved to a different scope.
"""

import ipaddress


def normalize(host):
    """Validate that host is a literal IPv6 address without brackets or a zone
    identifier and return its canonical string form.
    """
    if host is None:
        raise ValueError("ipv6 host is required")
    trimmed = host.strip()
    if trimmed == "":
        raise ValueError("ipv6 host is required")
    if trimmed.startswith("[") or trimmed.endswith("]"):
        raise ValueError("ipv6 host {!r} must be an unbracketed address".format(trimmed))
    if "%" in trimmed:
        raise ValueError("ipv6 host {!r} must not contain a zone identifier".format(trimmed))
    try:
        addr = ipaddress.ip_address(trimmed)
    except ValueError as exc:
        raise ValueError("ipv6 host {!r} is not a literal IPv6 address: {}".format(trimmed, exc))
    if not isinstance(addr, ipaddress.IPv6Address):
        raise ValueError("ipv6 host {!r} is not an IPv6 address".format(trimmed))
    if addr.ipv4_mapped is not None:
        raise ValueError("ipv6 host {!r} is an IPv4-mapped address".format(trimmed))
    return str(addr)


def addresses(host):
    """Return the single normalized candidate address for a literal IPv6 host."""
    return [normalize(host)]
