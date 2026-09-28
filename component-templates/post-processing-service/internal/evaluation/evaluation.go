// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package evaluation implements the reference evaluation strategy of the
// PostProcessingService: the paper-exact precision-based stopping criterion
// plus the deterministic test/demo policy knob.
//
// Statistical policy (the paper's criterion, verbatim): over the pooled
// mean_wait_time observations (one per completed replication, pooled across
// rounds) with n >= 1, compute the sample mean X-bar and the sample variance
// s^2 (two-pass: the mean first, then the sum of squared deviations about
// the mean, divided by n-1), and the half-width
//
//	h = t(0.975, n-1) * s / sqrt(n)
//
// of the 95% confidence interval, with the Student-t quantile from
// gonum.org/v1/gonum/stat/distuv. The request's confidence_metric is the
// desired precision threshold epsilon (absolute, in the metric's units);
// the criterion is met iff h <= epsilon. The sequential fixed-width
// additional-replication estimate is
//
//	n_req = ceil((t(0.975, n-1) * s / epsilon)^2)
//
// and the additional batch is max(min-batch, n_req - n) with min-batch =
// max(1, min(2, max-runners-per-round)), clamped to max-runners-per-round
// and to max-replications - n; a clamped result of 0, or n >=
// max-replications with the criterion unmet, is the stop_unmet verdict.
//
// Degenerate rule (the estimate cannot be formed): n = 0 or n = 1 counts as
// not-met with the minimum batch (h is undefined and reported as 0.0);
// s = 0 with n >= 2 counts as met with h = 0.
//
// Deterministic policy (deterministic-first-round-not-met, for tests and
// demos): runner_round 1 always answers not-met with a fixed
// additional-runners count (deterministic-additional-runners, clamped to
// max-runners-per-round, at least 1); runner_round >= 2 always answers met.
// sample_mean and half_width are reported as 0.0 and the request's
// number_of_reps is echoed as replications. This decouples the loop-machinery
// (messaging, round Jobs, verdict application) from the model's randomness;
// the statistical math itself is conformance-tested with hand-computed
// vectors.
package evaluation

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/stat/distuv"
)

// Policy selects the evaluation strategy.
type Policy string

// Policy constants.
const (
	// PolicyStatistical is the paper-exact precision-based criterion.
	PolicyStatistical Policy = "statistical"
	// PolicyDeterministicFirstRoundNotMet is the test/demo knob: round 1
	// answers not-met with a fixed additional-runner count, round >= 2
	// answers met.
	PolicyDeterministicFirstRoundNotMet Policy = "deterministic-first-round-not-met"
)

// ParsePolicy maps a policy name to its constant. Unknown names are an error.
func ParsePolicy(s string) (Policy, error) {
	switch Policy(s) {
	case PolicyStatistical, PolicyDeterministicFirstRoundNotMet:
		return Policy(s), nil
	default:
		return "", fmt.Errorf("evaluation policy %q must be %q or %q", s, PolicyStatistical, PolicyDeterministicFirstRoundNotMet)
	}
}

// Verdict strings shared with the wire contract.
const (
	VerdictMet               = "met"
	VerdictAdditionalRunners = "additional_runners"
	VerdictStopUnmet         = "stop_unmet"
)

// Params carries the policy and the loop-bound knobs.
type Params struct {
	// Policy selects the strategy.
	Policy Policy
	// DeterministicAdditionalRunners is the fixed additional-runner count of
	// the deterministic policy's first-round not-met verdict (clamped to
	// MaxRunnersPerRound at evaluation time, at least 1).
	DeterministicAdditionalRunners int
	// MaxReplications is the user-defined maximum total number of
	// replications across all rounds; reaching it with the criterion unmet
	// stops the scenario unmet.
	MaxReplications int
	// MaxRunnersPerRound is the per-round safety clamp on any additional
	// batch.
	MaxRunnersPerRound int
}

// Validate reports whether the loop-bound knobs are well-formed. The policy
// must be one of the two constants and every count must be positive.
func (p Params) Validate() error {
	if _, err := ParsePolicy(string(p.Policy)); err != nil {
		return err
	}
	if p.DeterministicAdditionalRunners < 1 {
		return fmt.Errorf("deterministic additional runners %d must be >= 1", p.DeterministicAdditionalRunners)
	}
	if p.MaxReplications < 1 {
		return fmt.Errorf("max replications %d must be >= 1", p.MaxReplications)
	}
	if p.MaxRunnersPerRound < 1 {
		return fmt.Errorf("max runners per round %d must be >= 1", p.MaxRunnersPerRound)
	}
	return nil
}

// Input is one evaluation invocation.
type Input struct {
	// RunnerRound is the scenario's current runner round (>= 1).
	RunnerRound int
	// NumberOfReps is the pooled replication count so far (>= 1); it is
	// echoed as replications by the deterministic policy.
	NumberOfReps int
	// ConfidenceMetric is the desired precision threshold epsilon (> 0,
	// finite, in the metric's units).
	ConfidenceMetric float64
	// Observations are the pooled mean_wait_time values, one per completed
	// replication. The statistical policy uses them; the deterministic
	// policy ignores them (the PPS does not query the Result DB for it).
	Observations []float64
	// Params carries the policy and loop bounds.
	Params Params
}

// Output is one evaluation outcome (the PPS fills the verdict payload's
// outcome fields from it).
type Output struct {
	// Verdict is met, additional_runners, or stop_unmet.
	Verdict string
	// SampleMean is X-bar over the pooled observations (0.0 for the
	// deterministic policy or when there are no observations).
	SampleMean float64
	// HalfWidth is h (0.0 when undefined: n < 2 or the deterministic
	// policy).
	HalfWidth float64
	// Replications is the pooled observation count n (the request's
	// number_of_reps under the deterministic policy).
	Replications int
	// AdditionalRunners is the requested additional batch (>= 1 iff Verdict
	// is additional_runners, else 0).
	AdditionalRunners int
}

// Evaluate runs the selected policy over the input and returns the outcome.
// The input's domain constraints (epsilon finite and > 0, round >= 1, reps
// >= 1) are enforced by the caller's wire validation; Evaluate validates the
// params defensively and reports a malformed observation set as an error.
func Evaluate(in Input) (Output, error) {
	if err := in.Params.Validate(); err != nil {
		return Output{}, fmt.Errorf("evaluation params: %w", err)
	}
	if in.RunnerRound < 1 {
		return Output{}, fmt.Errorf("runner round %d must be >= 1", in.RunnerRound)
	}
	if in.NumberOfReps < 1 {
		return Output{}, fmt.Errorf("number of reps %d must be >= 1", in.NumberOfReps)
	}
	if !isFinite(in.ConfidenceMetric) || in.ConfidenceMetric <= 0 {
		return Output{}, fmt.Errorf("confidence metric %v must be finite and > 0", in.ConfidenceMetric)
	}
	for _, v := range in.Observations {
		if !isFinite(v) {
			return Output{}, fmt.Errorf("observation %v is not finite", v)
		}
	}
	if in.Params.Policy == PolicyDeterministicFirstRoundNotMet {
		return evaluateDeterministic(in), nil
	}
	return evaluateStatistical(in), nil
}

// evaluateDeterministic implements the deterministic-first-round-not-met
// policy: round 1 is not-met with the fixed (clamped) additional-runner
// count, round >= 2 is met; zero-value statistics and the request's
// number_of_reps echoed as replications.
func evaluateDeterministic(in Input) Output {
	out := Output{
		SampleMean:   0.0,
		HalfWidth:    0.0,
		Replications: in.NumberOfReps,
	}
	if in.RunnerRound == 1 {
		out.Verdict = VerdictAdditionalRunners
		additional := in.Params.DeterministicAdditionalRunners
		if additional > in.Params.MaxRunnersPerRound {
			additional = in.Params.MaxRunnersPerRound
		}
		if additional < 1 {
			additional = 1
		}
		out.AdditionalRunners = additional
	} else {
		out.Verdict = VerdictMet
		out.AdditionalRunners = 0
	}
	return out
}

// evaluateStatistical implements the paper-exact precision criterion over
// the pooled observations.
func evaluateStatistical(in Input) Output {
	n := len(in.Observations)
	if n == 0 {
		// Degenerate: no completed replication yet. Not-met with the
		// minimum batch; the statistics are undefined and report 0.
		return Output{
			Verdict:           VerdictAdditionalRunners,
			SampleMean:        0.0,
			HalfWidth:         0.0,
			Replications:      0,
			AdditionalRunners: MinBatch(in.Params.MaxRunnersPerRound),
		}
	}
	mean, s := SampleMeanStd(in.Observations)
	if n == 1 {
		// Degenerate: with a single observation the half-width is
		// undefined; the criterion counts as not-met with the minimum
		// batch.
		return Output{
			Verdict:           VerdictAdditionalRunners,
			SampleMean:        mean,
			HalfWidth:         0.0,
			Replications:      n,
			AdditionalRunners: MinBatch(in.Params.MaxRunnersPerRound),
		}
	}
	h := HalfWidth(n, s)
	if h <= in.ConfidenceMetric {
		// s == 0 with n >= 2 reaches here with h = 0 and is met.
		return Output{
			Verdict:           VerdictMet,
			SampleMean:        mean,
			HalfWidth:         h,
			Replications:      n,
			AdditionalRunners: 0,
		}
	}
	// Not met.
	if n >= in.Params.MaxReplications {
		// The user-defined maximum number of replications is the
		// additional stopping criterion: stop unmet, all results
		// preserved in the Result DB.
		return Output{
			Verdict:           VerdictStopUnmet,
			SampleMean:        mean,
			HalfWidth:         h,
			Replications:      n,
			AdditionalRunners: 0,
		}
	}
	// Sequential fixed-width estimate: n_req = ceil((t * s / eps)^2). With
	// s > 0 (s == 0 was met above) the estimate is well-formed.
	nReq := RequiredReplications(n, s, in.ConfidenceMetric)
	additional := MinBatch(in.Params.MaxRunnersPerRound)
	if nReq-n > additional {
		additional = nReq - n
	}
	if additional > in.Params.MaxRunnersPerRound {
		additional = in.Params.MaxRunnersPerRound
	}
	if additional > in.Params.MaxReplications-n {
		additional = in.Params.MaxReplications - n
	}
	if additional <= 0 {
		// The clamped batch is empty: the total cap is exhausted.
		return Output{
			Verdict:           VerdictStopUnmet,
			SampleMean:        mean,
			HalfWidth:         h,
			Replications:      n,
			AdditionalRunners: 0,
		}
	}
	return Output{
		Verdict:           VerdictAdditionalRunners,
		SampleMean:        mean,
		HalfWidth:         h,
		Replications:      n,
		AdditionalRunners: additional,
	}
}

// SampleMeanStd computes the sample mean X-bar and sample standard
// deviation s over obs with the two-pass algorithm: the mean first, then
// s = sqrt(sum((x_i - X-bar)^2) / (n - 1)). For n = 0 it returns (0, 0);
// for n = 1 it returns (mean, 0) (the sample variance is undefined).
func SampleMeanStd(obs []float64) (mean, std float64) {
	n := len(obs)
	if n == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range obs {
		sum += v
	}
	mean = sum / float64(n)
	if n == 1 {
		return mean, 0
	}
	ss := 0.0
	for _, v := range obs {
		d := v - mean
		ss += d * d
	}
	std = math.Sqrt(ss / float64(n-1))
	return mean, std
}

// HalfWidth returns h = t(0.975, n-1) * s / sqrt(n), the half-width of the
// 95% confidence interval for n observations with sample standard deviation
// s. n must be >= 2; for s = 0 it returns 0.
func HalfWidth(n int, s float64) float64 {
	return tQuantile(float64(n-1)) * s / math.Sqrt(float64(n))
}

// RequiredReplications returns n_req = ceil((t(0.975, n-1) * s / eps)^2),
// the sequential fixed-width estimate of the total replications needed for a
// 95% confidence interval half-width of at most eps. n must be >= 2 and s
// and eps positive; with s = 0 it returns 0.
func RequiredReplications(n int, s, eps float64) int {
	r := tQuantile(float64(n-1)) * s / eps
	return int(math.Ceil(r * r))
}

// MinBatch returns the minimum additional batch: max(1, min(2,
// maxRunnersPerRound)). The caller guarantees maxRunnersPerRound >= 1, so
// the result is 1 for maxRunnersPerRound = 1 and 2 otherwise.
func MinBatch(maxRunnersPerRound int) int {
	if maxRunnersPerRound < 1 {
		return 1
	}
	if maxRunnersPerRound > 2 {
		return 2
	}
	return maxRunnersPerRound
}

// tQuantile returns the 0.975 quantile of the standard Student-t
// distribution with df degrees of freedom (df >= 1) via gonum's distuv
// package. StudentsT's zero-value Sigma is the scale parameter (0 scales the
// distribution to a point mass), so the standard distribution is
// StudentsT{Nu: df, Sigma: 1}.
func tQuantile(df float64) float64 {
	return distuv.StudentsT{Nu: df, Sigma: 1}.Quantile(0.975)
}

// isFinite reports whether x is neither NaN nor +/-Inf.
func isFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
