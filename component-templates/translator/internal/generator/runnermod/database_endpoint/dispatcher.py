"""Common database endpoint contract dispatcher for the runner.

Classifies a database host as a DNS name or an IPv4/IPv6 literal, resolves DNS
hosts to an ordered, de-duplicated address list through the dns module, and
dials candidate addresses in resolver order under a shared 10-second deadline;
the first successful connection owns the dial and remaining addresses are not
contacted. The connection is NOT closed by dial: the caller owns the
connection's lifecycle.

This module is transport-agnostic: the caller supplies a connector callable
``connect(host, port, user, password, dbname, timeout_seconds) -> conn`` so the
dispatcher is independently testable without a real PostgreSQL driver. The
runner wires a psycopg-based connector for its Result DB client.
"""

import ipaddress
import re
import time

from . import dns, ipv4, ipv6

HOST_DNS = "dns"
HOST_IPV4 = "ipv4"
HOST_IPV6 = "ipv6"

_DNS_LABEL = r"[a-z0-9]([-a-z0-9]*[a-z0-9])?"
_DNS_SUBDOMAIN_RE = re.compile(r"^{}(\.{})*$".format(_DNS_LABEL, _DNS_LABEL))


def _is_dns_subdomain(s):
    if not s or len(s) > 253:
        return False
    return _DNS_SUBDOMAIN_RE.match(s) is not None


def classify_host(host):
    """Trim and classify a database host per the alpha4 common endpoint contract.

    Returns a (kind, normalized) tuple where kind is one of HOST_DNS, HOST_IPV4,
    HOST_IPV6 and normalized is the canonical host string. Empty, bracketed,
    zone-scoped, URL-scheme, path, query, fragment, embedded-port, and
    Unix-socket hosts raise ValueError.
    """
    if host is None:
        raise ValueError("database host is required")
    trimmed = host.strip()
    if trimmed == "":
        raise ValueError("database host is required")
    if trimmed.startswith("[") or trimmed.endswith("]"):
        raise ValueError("database host {!r} must be an unbracketed address".format(trimmed))
    if any(c in trimmed for c in "/?#"):
        raise ValueError("database host {!r} must not contain a scheme, path, query, or fragment".format(trimmed))
    if "%" in trimmed:
        raise ValueError("database host {!r} must not contain an IPv6 zone identifier".format(trimmed))
    try:
        addr = ipaddress.ip_address(trimmed)
    except ValueError:
        addr = None
    if addr is not None:
        if isinstance(addr, ipaddress.IPv4Address):
            return (HOST_IPV4, str(addr))
        if isinstance(addr, ipaddress.IPv6Address):
            if addr.ipv4_mapped is not None:
                raise ValueError("database host {!r} is an IPv4-mapped address".format(trimmed))
            return (HOST_IPV6, str(addr))
    # Not a literal address. A DNS subdomain has no colon.
    if ":" in trimmed:
        raise ValueError("database host {!r} must not contain an embedded port".format(trimmed))
    if not _is_dns_subdomain(trimmed):
        raise ValueError("database host {!r} is not a valid lowercase DNS subdomain".format(trimmed))
    return (HOST_DNS, trimmed)


def resolve_addresses(kind, host, resolver=None):
    """Resolve a classified database host to an ordered, de-duplicated list of
    canonical IP address strings. A literal IPv4 or IPv6 address yields a single
    normalized address without DNS. A DNS host is resolved anew through the
    resolver (None uses the process getaddrinfo).
    """
    if kind == HOST_IPV4:
        return ipv4.addresses(host)
    if kind == HOST_IPV6:
        return ipv6.addresses(host)
    return dns.resolve(host, resolver)


def dial(ep, resolver=None, connector=None, deadline=None):
    """Classify the endpoint, resolve it, and dial candidate addresses in
    resolver order under a shared deadline. The first successful connection
    owns the dial; remaining addresses are not contacted.

    ``ep`` is a mapping with keys host, port, user, password, dbname.
    ``connector`` is a callable
    ``connect(host, port, user, password, dbname, timeout_seconds) -> conn``
    and is required. ``deadline`` is the shared deadline in seconds (defaults
    to RESOLUTION_DEADLINE_SECONDS). Returns a dict with conn, host, and kind.
    """
    from . import RESOLUTION_DEADLINE_SECONDS

    if connector is None:
        raise ValueError("database dial requires a connector")
    if deadline is None:
        deadline = RESOLUTION_DEADLINE_SECONDS
    kind, normalized = classify_host(ep["host"])
    addresses = resolve_addresses(kind, normalized, resolver)
    deadline_ts = time.monotonic() + deadline
    last_err = None
    for host in addresses:
        remaining = deadline_ts - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("database dial exceeded the shared deadline")
        try:
            conn = connector(host, ep["port"], ep.get("user"), ep.get("password"), ep.get("dbname"), remaining)
        except Exception as exc:  # noqa: BLE001 - any connect failure tries the next address
            last_err = exc
            # If the failed attempt consumed the shared deadline, the dial is
            # timed out rather than merely exhausted of addresses.
            if time.monotonic() >= deadline_ts:
                raise TimeoutError("database dial exceeded the shared deadline") from exc
            continue
        return {"conn": conn, "host": host, "kind": kind}
    raise ValueError("connect database {}:{}: {}".format(normalized, ep["port"], last_err))
