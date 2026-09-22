"""DNS endpoint module for the common database endpoint contract.

A DNS host is resolved anew through the resolver for both A and AAAA records;
results retain resolver order and duplicate normalized addresses are removed
by retaining their first occurrence. An empty result or a resolver error is a
resolution failure. The host is never re-resolved once a connection is
established, so each candidate address is a fresh snapshot for one dial
attempt.
"""

import ipaddress
import socket


def validate_host(host):
    """Reject obviously non-DNS hosts: empty, bracketed, zone-scoped, whitespace,
    or containing a colon (a DNS subdomain has no colon).
    """
    if host is None:
        raise ValueError("dns host is required")
    if host == "":
        raise ValueError("dns host is required")
    if host != host.strip():
        raise ValueError("dns host {!r} must not contain surrounding whitespace".format(host))
    if any(c in host for c in "[] \t"):
        raise ValueError("dns host {!r} must be a bare hostname".format(host))
    if "%" in host:
        raise ValueError("dns host {!r} must not contain a zone identifier".format(host))
    if ":" in host:
        raise ValueError("dns host {!r} must not contain an embedded port".format(host))


def _default_resolver(host):
    """Resolve host through the process getaddrinfo for both A and AAAA records
    and return a list of canonical IP address strings in resolver order.
    """
    out = []
    try:
        infos = socket.getaddrinfo(host, None, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        raise ValueError("resolve database host {!r}: {}".format(host, exc))
    for family, _stype, _proto, _canon, sockaddr in infos:
        ip = sockaddr[0]
        try:
            normalized = str(ipaddress.ip_address(ip))
        except ValueError:
            continue
        out.append(normalized)
    return out


def resolve(host, resolver=None):
    """Resolve host to an ordered, de-duplicated list of canonical IP address
    strings. A None resolver uses the process getaddrinfo. A custom resolver is
    a callable taking the host and returning a list of address strings. An
    empty result or a resolver error raises ValueError.
    """
    validate_host(host)
    if resolver is None:
        addrs = _default_resolver(host)
    else:
        addrs = resolver(host)
        if addrs is None:
            addrs = []
    seen = set()
    out = []
    for raw in addrs:
        if raw is None:
            continue
        try:
            normalized = str(ipaddress.ip_address(raw))
        except ValueError:
            continue
        if normalized in seen:
            continue
        seen.add(normalized)
        out.append(normalized)
    if not out:
        raise ValueError("database host {!r} resolved to no addresses".format(host))
    return out
