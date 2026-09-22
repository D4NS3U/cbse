"""database_endpoint package: the runner's common database endpoint contract.

The dispatcher classifies a database host as a DNS name or an IPv4/IPv6
literal, resolves DNS hosts to an ordered, de-duplicated address list, and
dials candidate addresses in resolver order under a shared 10-second deadline;
the first successful connection owns the operation. This mirrors the Go
contracts shipped by the Experiment Operator probe and the Translator
template's internal/databaseendpoint dispatcher so all three clients classify,
order, de-duplicate, time-bound, and fall back identically.
"""

RESOLUTION_DEADLINE_SECONDS = 10.0
