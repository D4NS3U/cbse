"""Single-server queue SimPy model for the reference example generator.

A source process samples interarrival times with rng.expovariate(arrival_rate)
and creates customers only while their sampled arrival time is less than
run_duration. Each customer records the time spent waiting for a single
simpy.Resource(capacity=1), samples its service time with
rng.expovariate(service_rate), and increments the completed count only if its
service finishes by the simulation horizon. The environment runs until
run_duration; unfinished customers are neither completed nor included in the
mean. mean_wait_time is the arithmetic mean for completed customers and 0.0
when no customer completes.
"""

import simpy


def run(arrival_rate, service_rate, run_duration, rng):
    """Run the single-server queue and return (completed_customers, mean_wait_time).

    arrival_rate, service_rate, and run_duration are positive integers; rng is a
    local random.Random instance the caller derived from the seed policy and the
    runtime hostname.
    """
    env = simpy.Environment()
    resource = simpy.Resource(env, capacity=1)

    state = {"completed": 0, "waits": []}

    def customer(arrival_time):
        yield env.timeout(arrival_time)
        request = resource.request()
        yield request
        wait = env.now - arrival_time
        service_time = rng.expovariate(service_rate)
        finish = env.now + service_time
        if finish <= run_duration:
            yield env.timeout(service_time)
            state["waits"].append(wait)
            state["completed"] += 1
        else:
            yield env.timeout(service_time)
        resource.release(request)

    def source():
        t = 0.0
        while True:
            inter = rng.expovariate(arrival_rate)
            t += inter
            if t >= run_duration:
                break
            env.process(customer(t))

    env.process(source())
    env.run(until=run_duration)

    completed = state["completed"]
    mean = sum(state["waits"]) / len(state["waits"]) if state["waits"] else 0.0
    return completed, mean
