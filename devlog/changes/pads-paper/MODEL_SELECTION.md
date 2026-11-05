# Model selection for the first PADS paper results

## Recommendation

Use an **impatient-customer multi-server queue (M/M/c+M, Erlang-A)**, implemented as a small CBSE-specific generalization of SimPy's published **Bank Renege** example.

This recommendation balances four needs:

1. **Recognizable research model.** Erlang-A is an established queueing model for service systems with customer abandonment.
2. **Published executable anchor.** SimPy publishes a concise reneging-customer example that uses `Resource` and condition events.
3. **Low integration cost.** CBSE's existing `arrival_rate`, `service_rate`, `run_duration`, `seed_policy`, and `mean_wait_time` contracts remain valid.
4. **Understandability.** The event logic fits on one page: arrivals, server request, patience timeout, service, and metrics.

## Source provenance

### SimPy implementation anchor

The historical official SimPy GitHub mirror contains the Bank Renege example at a stable commit:

- Repository: <https://github.com/simpx/simpy>
- Stable repository commit: [`9ba33ff8e8f23391f27572d03e4fe874bbf5f12c`](https://github.com/simpx/simpy/commit/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c)
- Example explanation: [`docs/examples/bank_renege.rst`](https://github.com/simpx/simpy/blob/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c/docs/examples/bank_renege.rst)
- Example code: [`docs/examples/code/bank_renege.py`](https://github.com/simpx/simpy/blob/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c/docs/examples/code/bank_renege.py)
- License: [MIT](https://github.com/simpx/simpy/blob/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c/LICENSE.rst)

The GitHub mirror is historical. The current SimPy project source is hosted on GitLab, and CBSE's runner base pins `simpy==4.1.2`:

- SimPy source: <https://gitlab.com/team-simpy/simpy/>
- SimPy 4.1.2 Bank Renege source: <https://gitlab.com/team-simpy/simpy/-/blob/4.1.2/docs/examples/code/bank_renege.py>
- Package metadata: <https://pypi.org/project/simpy/4.1.2/>

The CBSE implementation should be written to CBSE's deterministic RNG, no-event-logging, result-record, and test contracts. The SimPy example is the behavioral and citation anchor. If source text is copied rather than independently adapted, its MIT notice must be retained alongside the repository's Apache-2.0 notices.

### Academic anchors

Primary citation:

> O. Garnett, A. Mandelbaum, and M. Reiman, “Designing a Call Center with Impatient Customers,” *Manufacturing & Service Operations Management*, vol. 4, no. 3, pp. 208–227, 2002. DOI: [10.1287/msom.4.3.208.7753](https://doi.org/10.1287/msom.4.3.208.7753).

Accessible model overview:

> A. Mandelbaum and S. Zeltyn, “Service Engineering in Action: The Palm/Erlang-A Queue, with Applications to Call Centers,” in *Advances in Services Innovations*, pp. 17–45. DOI: [10.1007/978-3-540-29860-1_2](https://doi.org/10.1007/978-3-540-29860-1_2).

Additional experiment-interpretation source:

> W. Whitt, “Sensitivity of Performance in the Erlang-A Queueing Model to Changes in the Model Parameters,” *Operations Research*, vol. 54, no. 2, pp. 247–260, 2006. DOI: [10.1287/opre.1050.0257](https://doi.org/10.1287/opre.1050.0257).

#### Literature and model semantics

In `M/M/c+M`, the first `M` denotes Poisson arrivals (equivalently, exponential interarrival times), the second denotes exponential service requirements, and `c` is the number of parallel servers. The `+M` adds an independent exponential patience clock for each waiting customer: a customer whose clock expires before service begins abandons the queue. This Markovian impatient-customer model is commonly called Erlang-A or the Palm queue.

Abandonment materially changes overload behavior. Without abandonment, an unlimited-waiting-room M/M/c queue has no steady state when `λ >= cμ`; offered work then exceeds nominal service capacity and the queue can grow without bound. In Erlang-A, if `q` customers are waiting, their aggregate abandonment rate is `qθ`. That state-dependent departure pressure grows with the queue and makes the steady-state model stable for any finite `λ` when `θ > 0`. Thus `ρ = λ/(cμ) = 1` remains a useful *service-capacity* reference, but is not an Erlang-A stability boundary: beyond it, excess demand is increasingly expressed through waiting and abandonment rather than an unbounded steady-state queue.

The sources play complementary roles. Garnett, Mandelbaum, and Reiman analyze the unlimited-waiting-room M/M/N+M call-center model with exponential patience. Mandelbaum and Zeltyn provide an accessible Palm/Erlang-A service-engineering treatment. Whitt studies the sensitivity of Erlang-A performance measures to `λ`, `μ`, `c`, and `θ`, finding much stronger sensitivity to arrival and service rates than to the abandonment rate in the regimes studied. That result motivates CBSE's initial response-surface experiment: vary `λ` and `μ`, hold `c=2` and `θ=1` fixed, and interpret the resulting wait and abandonment measures around the nominal `ρ=1` boundary. It is qualitative support for the experiment design, not a claim that the finite-horizon two-server simulation reproduces Whitt's large-system numerical results.

The bibliographic metadata above was checked against the Crossref DOI records and publisher landing pages: [Garnett et al. (2002)](https://pubsonline.informs.org/doi/10.1287/msom.4.3.208.7753), [Mandelbaum and Zeltyn](https://link.springer.com/chapter/10.1007/978-3-540-29860-1_2), and [Whitt (2006)](https://pubsonline.informs.org/doi/10.1287/opre.1050.0257).

The paper should cite both the queueing-model literature and SimPy as the implementation framework. It should describe the CBSE model as an independently parameterized Erlang-A implementation inspired by the published SimPy reneging pattern, not as an unmodified upstream benchmark.

The distinction from the executable anchor is exact: upstream Bank Renege has one `Resource` slot and draws patience from a uniform distribution, whereas `erlang_a_v1` uses two servers and exponential patience with rate `θ=1`. Bank Renege demonstrates the SimPy request-versus-timeout event pattern; it is not itself the CBSE Erlang-A model.

## Candidate comparison

| Candidate | Published code | Established problem | Existing CBSE parameter fit | Scientific richness | Implementation burden | Decision |
|---|---|---|---|---|---|---|
| **M/M/c+M impatient queue** | SimPy Bank Renege on GitHub/current source | Erlang-A / Palm queue | Excellent: λ, μ, T, seed map directly | Parallel service, congestion, abandonment, adaptive replication | Low | **Selected** |
| SimPy Machine Shop | [GitHub example](https://github.com/simpx/simpy/blob/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c/docs/examples/code/machine_shop.py) | Machine-repairman / interference problem | Poor: needs MTTF, repair time, machine count, process time | Interrupts and preemptive repair are attractive | Medium/high because EDS/DB names and PPS metric no longer fit | Rejected for first results; good later case study |
| SimPy Gas Station Refueling | [GitHub example](https://github.com/simpx/simpy/blob/9ba33ff8e8f23391f27572d03e4fe874bbf5f12c/docs/examples/code/gas_station_refuel.py) | Queue plus shared inventory/replenishment | Partial: arrival/service fit, tank parameters do not | Resource + Container + replenishment | Medium; would hide important fixed inventory assumptions | Rejected for first results |
| Classical job-shop scheduling | Many public repositories and benchmark sets | Fisher–Thompson/OR scheduling benchmark family | Poor: requires job-operation matrices and scheduling policies, not four scalar parameters | Very high | High; requires a generic design schema or external instance files | Rejected for first results; too large for the current EDS contract |
| Existing M/M/1 queue | Already implemented in CBSE | Textbook queue | Excellent | Too limited for a stronger paper demonstration | None | Replace on `PADS-paper` |

## Why not the Machine Shop for the first paper result?

The SimPy Machine Shop is the strongest alternative from a DES-mechanics perspective. It demonstrates interrupts and a `PreemptiveResource`, and the machine-repairman problem is well established. Its natural factors, however, are machine count, mean processing time, mean time to failure, repair time, and simulation horizon. Mapping those onto fields named `arrival_rate` and `service_rate` would be misleading, while renaming/generalizing the EDS and Detail DB would expand the branch far beyond the requested first-results model.

It should remain a candidate for a later “second model” portability demonstration after CBSE has a generic parameter schema.

## Why the selected model is more than the current queue

The current reference model is an M/M/1-style finite-horizon queue with a single server and no abandonment. The selected model adds:

- multiple server capacity (`c=2`);
- a second stochastic clock per customer (patience);
- a condition event (`request | timeout`);
- two terminal customer outcomes (served or abandoned);
- warm-up and drain phases;
- utilization, throughput, and abandonment responses;
- a meaningful capacity boundary `ρ = λ/(cμ)` that can be crossed in a compact experiment.

These additions produce interpretable nonlinear behavior while retaining a small implementation and the current EDS/PPS contracts.

## Attribution wording for the paper

Suggested wording:

> We implemented a finite-horizon M/M/c+M impatient-customer queue in SimPy 4.1.2. The event structure generalizes SimPy's published “Bank Renege” example from a single counter to a fixed two-server Erlang-A system, while CBSE supplies scenario parameterization, isolated replications, result persistence, and sequential fixed-width precision control.

Do not describe the SimPy example itself as an Erlang-A benchmark: its published code uses a single counter and uniformly distributed patience. Erlang-A is the CBSE generalization with exponential patience and multiple servers.
