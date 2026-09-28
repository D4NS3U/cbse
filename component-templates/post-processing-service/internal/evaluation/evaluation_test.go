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

package evaluation

import (
	"math"
	"testing"
)

// testParams is the statistical policy with the canonical loop bounds.
func testParams() Params {
	return Params{
		Policy:                         PolicyStatistical,
		DeterministicAdditionalRunners: 2,
		MaxReplications:                10000,
		MaxRunnersPerRound:             1000,
	}
}

// alternating returns n observations alternating 0.0 and 1.0 (starting with
// 0.0). For odd n the mean is ~0.5 and the sample std ~0.5; for even n the
// mean is exactly 0.5 and the sample std just under 0.5.
func alternating(n int) []float64 {
	obs := make([]float64, n)
	for i := range obs {
		if i%2 == 1 {
			obs[i] = 1.0
		}
	}
	return obs
}

func closeEnough(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// TestStatisticalMet is the hand-computed met vector. n = 41 observations
// (twenty -0.5, twenty 0.5, one 0.0): mean X-bar = 0, sample variance
// s^2 = (20*0.25 + 20*0.25 + 0) / 40 = 0.25, s = 0.5. With epsilon = 0.5
// (the paper's absolute threshold) and df = 40, t(0.975, 40) = 2.021075:
//
//	h = t * s / sqrt(n) = 2.021075 * 0.5 / sqrt(41) = 1.0105375 / 6.4031242
//	  = 0.157821 <= 0.5  =>  met.
func TestStatisticalMet(t *testing.T) {
	obs := make([]float64, 41)
	for i := 0; i < 20; i++ {
		obs[i] = -0.5
		obs[i+20] = 0.5
	}
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     41,
		ConfidenceMetric: 0.5,
		Observations:     obs,
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictMet {
		t.Fatalf("verdict = %q, want met", out.Verdict)
	}
	if out.AdditionalRunners != 0 {
		t.Fatalf("additional = %d, want 0", out.AdditionalRunners)
	}
	if out.Replications != 41 {
		t.Fatalf("replications = %d, want 41", out.Replications)
	}
	if !closeEnough(out.SampleMean, 0.0, 1e-12) {
		t.Fatalf("sample mean = %v, want 0", out.SampleMean)
	}
	if !closeEnough(out.HalfWidth, 0.157821, 1e-5) {
		t.Fatalf("half width = %v, want 0.157821 (t(0.975,40)*0.5/sqrt(41))", out.HalfWidth)
	}
}

// TestStatisticalNotMetWithNReq is the hand-computed not-met vector with the
// paper's epsilon = 0.5. n = 2 observations {0, 2}: mean = 1, s^2 =
// ((0-1)^2 + (2-1)^2)/1 = 2, s = sqrt(2) = 1.4142136. With df = 1,
// t(0.975, 1) = 12.706205:
//
//	h = t * s / sqrt(2) = 12.706205 * 1.4142136 / 1.4142136
//	  = 12.706205 > 0.5  =>  not met.
//
//	n_req = ceil((t * s / epsilon)^2)
//	      = ceil((12.706205 * 1.4142136 / 0.5)^2)
//	      = ceil(35.938558^2) = ceil(1291.58) = 1292.
//
//	additional = max(min-batch 2, 1292 - 2) = 1290, clamped to
//	max-runners-per-round 1000 (and to 10000 - 2 = 9998) => 1000.
func TestStatisticalNotMetWithNReq(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     2,
		ConfidenceMetric: 0.5,
		Observations:     []float64{0, 2},
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners {
		t.Fatalf("verdict = %q, want additional_runners", out.Verdict)
	}
	if out.AdditionalRunners != 1000 {
		t.Fatalf("additional = %d, want 1000 (n_req 1292 - 2 = 1290 clamped to max-runners-per-round)", out.AdditionalRunners)
	}
	if out.Replications != 2 {
		t.Fatalf("replications = %d, want 2", out.Replications)
	}
	if !closeEnough(out.SampleMean, 1.0, 1e-12) {
		t.Fatalf("sample mean = %v, want 1", out.SampleMean)
	}
	if !closeEnough(out.HalfWidth, 12.706205, 1e-5) {
		t.Fatalf("half width = %v, want 12.706205 (t(0.975,1)*s/sqrt(2) with s = sqrt(2))", out.HalfWidth)
	}
}

// TestStatisticalRequiredReplications hand-checks the estimator on the same
// n = 2, s = sqrt(2), epsilon = 0.5 inputs: n_req = 1292.
func TestStatisticalRequiredReplications(t *testing.T) {
	got := RequiredReplications(2, math.Sqrt2, 0.5)
	if got != 1292 {
		t.Fatalf("n_req = %d, want 1292 = ceil((12.706205*sqrt(2)/0.5)^2)", got)
	}
}

// TestStatisticalMaxReplicationsClamp is the hand-computed total-cap clamp
// vector. n = 9999 observations (alternating 0/1, mean ~0.5, s ~0.5) with
// epsilon = 0.001 and max-replications = 10000: h ~ 1.984199*0.5/99.995
// ~ 0.00992 > 0.001 so the criterion is unmet, n < max-replications, and the
// estimate-based batch is clamped to max-replications - n = 1.
func TestStatisticalMaxReplicationsClamp(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      2,
		NumberOfReps:     9999,
		ConfidenceMetric: 0.001,
		Observations:     alternating(9999),
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners {
		t.Fatalf("verdict = %q, want additional_runners", out.Verdict)
	}
	if out.AdditionalRunners != 1 {
		t.Fatalf("additional = %d, want 1 (clamped to max-replications - n)", out.AdditionalRunners)
	}
	if out.Replications != 9999 {
		t.Fatalf("replications = %d, want 9999", out.Replications)
	}
}

// TestStatisticalStopUnmet is the hand-computed stop vector. n = 10000
// observations (alternating 0/1, mean 0.5, s ~0.5) with epsilon = 0.001 and
// max-replications = 10000: h ~ 1.984199*0.5/100 ~ 0.00992 > 0.001, and
// n >= max-replications, so the user-defined maximum number of replications
// stops the scenario unmet with additional_runners 0.
func TestStatisticalStopUnmet(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      3,
		NumberOfReps:     10000,
		ConfidenceMetric: 0.001,
		Observations:     alternating(10000),
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictStopUnmet {
		t.Fatalf("verdict = %q, want stop_unmet", out.Verdict)
	}
	if out.AdditionalRunners != 0 {
		t.Fatalf("additional = %d, want 0", out.AdditionalRunners)
	}
	if out.Replications != 10000 {
		t.Fatalf("replications = %d, want 10000", out.Replications)
	}
	if !closeEnough(out.SampleMean, 0.5, 1e-9) {
		t.Fatalf("sample mean = %v, want 0.5", out.SampleMean)
	}
}

// TestStatisticalSingleObservation is the degenerate n = 1 vector: the
// half-width is undefined, the criterion counts as not-met with the minimum
// batch (max(1, min(2, 1000)) = 2), and the single observation is the
// sample mean.
func TestStatisticalSingleObservation(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     1,
		ConfidenceMetric: 0.5,
		Observations:     []float64{5},
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners {
		t.Fatalf("verdict = %q, want additional_runners", out.Verdict)
	}
	if out.AdditionalRunners != 2 {
		t.Fatalf("additional = %d, want min-batch 2", out.AdditionalRunners)
	}
	if out.Replications != 1 {
		t.Fatalf("replications = %d, want 1", out.Replications)
	}
	if !closeEnough(out.SampleMean, 5.0, 1e-12) {
		t.Fatalf("sample mean = %v, want 5", out.SampleMean)
	}
	if out.HalfWidth != 0 {
		t.Fatalf("half width = %v, want 0 (undefined for n = 1)", out.HalfWidth)
	}
}

// TestStatisticalNoObservations is the degenerate n = 0 vector (empty result
// table): not-met with the minimum batch and zero-value statistics.
func TestStatisticalNoObservations(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     1,
		ConfidenceMetric: 0.5,
		Observations:     nil,
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners {
		t.Fatalf("verdict = %q, want additional_runners", out.Verdict)
	}
	if out.AdditionalRunners != 2 {
		t.Fatalf("additional = %d, want min-batch 2", out.AdditionalRunners)
	}
	if out.Replications != 0 || out.SampleMean != 0 || out.HalfWidth != 0 {
		t.Fatalf("stats = %v/%v/%d, want 0/0/0", out.SampleMean, out.HalfWidth, out.Replications)
	}
}

// TestStatisticalZeroVarianceMet is the degenerate s = 0, n >= 2 vector: all
// observations identical, h = 0 <= epsilon, met with zero additional.
func TestStatisticalZeroVarianceMet(t *testing.T) {
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     4,
		ConfidenceMetric: 0.5,
		Observations:     []float64{2, 2, 2, 2},
		Params:           testParams(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictMet {
		t.Fatalf("verdict = %q, want met", out.Verdict)
	}
	if out.AdditionalRunners != 0 {
		t.Fatalf("additional = %d, want 0", out.AdditionalRunners)
	}
	if !closeEnough(out.SampleMean, 2.0, 1e-12) || out.HalfWidth != 0 {
		t.Fatalf("stats = %v/%v, want 2/0", out.SampleMean, out.HalfWidth)
	}
}

// TestStatisticalPerRoundClamp hand-checks the per-round clamp: the n = 2,
// s = sqrt(2), epsilon = 0.5 vector of TestStatisticalNotMetWithNReq (n_req
// 1292, raw additional 1290) with max-runners-per-round 50 clamps to 50.
func TestStatisticalPerRoundClamp(t *testing.T) {
	params := testParams()
	params.MaxRunnersPerRound = 50
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     2,
		ConfidenceMetric: 0.5,
		Observations:     []float64{0, 2},
		Params:           params,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners || out.AdditionalRunners != 50 {
		t.Fatalf("verdict/additional = %q/%d, want additional_runners/50", out.Verdict, out.AdditionalRunners)
	}
}

// TestStatisticalMinBatchOneRunnerPerRound checks min-batch with
// max-runners-per-round 1: the minimum batch is max(1, min(2, 1)) = 1, so the
// n = 1 degenerate case asks for exactly one additional runner.
func TestStatisticalMinBatchOneRunnerPerRound(t *testing.T) {
	params := testParams()
	params.MaxRunnersPerRound = 1
	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     1,
		ConfidenceMetric: 0.5,
		Observations:     []float64{5},
		Params:           params,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.AdditionalRunners != 1 {
		t.Fatalf("additional = %d, want min-batch 1", out.AdditionalRunners)
	}
}

func TestSampleMeanStd(t *testing.T) {
	// {1, 2, 3}: mean 2, s^2 = (1 + 0 + 1)/2 = 1, s = 1.
	mean, std := SampleMeanStd([]float64{1, 2, 3})
	if !closeEnough(mean, 2, 1e-12) || !closeEnough(std, 1, 1e-12) {
		t.Fatalf("mean/std = %v/%v, want 2/1", mean, std)
	}
	mean, std = SampleMeanStd(nil)
	if mean != 0 || std != 0 {
		t.Fatalf("empty mean/std = %v/%v, want 0/0", mean, std)
	}
	mean, std = SampleMeanStd([]float64{7})
	if !closeEnough(mean, 7, 1e-12) || std != 0 {
		t.Fatalf("single mean/std = %v/%v, want 7/0", mean, std)
	}
}

func TestMinBatch(t *testing.T) {
	if MinBatch(1) != 1 {
		t.Fatalf("min-batch(1) = %d, want 1", MinBatch(1))
	}
	if MinBatch(2) != 2 {
		t.Fatalf("min-batch(2) = %d, want 2", MinBatch(2))
	}
	if MinBatch(1000) != 2 {
		t.Fatalf("min-batch(1000) = %d, want 2", MinBatch(1000))
	}
}

func TestHalfWidth(t *testing.T) {
	// n = 2, s = sqrt(2): h = t(0.975, 1) * sqrt(2) / sqrt(2) = 12.706205.
	if !closeEnough(HalfWidth(2, math.Sqrt2), 12.706205, 1e-5) {
		t.Fatalf("h = %v, want 12.706205", HalfWidth(2, math.Sqrt2))
	}
	// s = 0 => h = 0.
	if HalfWidth(4, 0) != 0 {
		t.Fatalf("h = %v, want 0", HalfWidth(4, 0))
	}
}

// TestDeterministicPolicy covers the deterministic-first-round-not-met knob:
// round 1 answers not-met with the fixed (clamped) count, round >= 2 answers
// met, both with zero-value statistics and the request's number_of_reps
// echoed as replications.
func TestDeterministicPolicy(t *testing.T) {
	params := Params{
		Policy:                         PolicyDeterministicFirstRoundNotMet,
		DeterministicAdditionalRunners: 2,
		MaxReplications:                10000,
		MaxRunnersPerRound:             1000,
	}

	out, err := Evaluate(Input{
		RunnerRound:      1,
		NumberOfReps:     40,
		ConfidenceMetric: 0.5,
		Observations:     nil,
		Params:           params,
	})
	if err != nil {
		t.Fatalf("evaluate round 1: %v", err)
	}
	if out.Verdict != VerdictAdditionalRunners || out.AdditionalRunners != 2 {
		t.Fatalf("round 1 = %q/%d, want additional_runners/2", out.Verdict, out.AdditionalRunners)
	}
	if out.Replications != 40 || out.SampleMean != 0 || out.HalfWidth != 0 {
		t.Fatalf("round 1 echoes = %d/%v/%v, want 40/0/0", out.Replications, out.SampleMean, out.HalfWidth)
	}

	out, err = Evaluate(Input{
		RunnerRound:      2,
		NumberOfReps:     99,
		ConfidenceMetric: 0.5,
		Observations:     nil,
		Params:           params,
	})
	if err != nil {
		t.Fatalf("evaluate round 2: %v", err)
	}
	if out.Verdict != VerdictMet || out.AdditionalRunners != 0 {
		t.Fatalf("round 2 = %q/%d, want met/0", out.Verdict, out.AdditionalRunners)
	}
	if out.Replications != 99 {
		t.Fatalf("round 2 replications = %d, want 99", out.Replications)
	}

	out, err = Evaluate(Input{
		RunnerRound:      3,
		NumberOfReps:     199,
		ConfidenceMetric: 0.5,
		Observations:     nil,
		Params:           params,
	})
	if err != nil {
		t.Fatalf("evaluate round 3: %v", err)
	}
	if out.Verdict != VerdictMet {
		t.Fatalf("round 3 = %q, want met", out.Verdict)
	}
}

// TestDeterministicPolicyClamp checks the clamp of the fixed count to
// max-runners-per-round: 5000 with max-runners-per-round 1000 becomes 1000,
// and 1 stays 1.
func TestDeterministicPolicyClamp(t *testing.T) {
	for _, det := range []int{5000, 1} {
		params := Params{
			Policy:                         PolicyDeterministicFirstRoundNotMet,
			DeterministicAdditionalRunners: det,
			MaxReplications:                10000,
			MaxRunnersPerRound:             1000,
		}
		out, err := Evaluate(Input{
			RunnerRound:      1,
			NumberOfReps:     10,
			ConfidenceMetric: 0.5,
			Observations:     nil,
			Params:           params,
		})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		want := det
		if want > 1000 {
			want = 1000
		}
		if out.Verdict != VerdictAdditionalRunners || out.AdditionalRunners != want {
			t.Fatalf("det=%d: %q/%d, want additional_runners/%d", det, out.Verdict, out.AdditionalRunners, want)
		}
	}
}

func TestParsePolicy(t *testing.T) {
	if p, err := ParsePolicy("statistical"); err != nil || p != PolicyStatistical {
		t.Fatalf("statistical: %v/%v", p, err)
	}
	if p, err := ParsePolicy("deterministic-first-round-not-met"); err != nil || p != PolicyDeterministicFirstRoundNotMet {
		t.Fatalf("deterministic: %v/%v", p, err)
	}
	for _, s := range []string{"", "Statistical", "deterministic", "first-round-not-met", "statistical "} {
		if _, err := ParsePolicy(s); err == nil {
			t.Fatalf("policy %q accepted", s)
		}
	}
}

func TestParamsValidate(t *testing.T) {
	valid := testParams()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Params)
	}{
		{"unknown policy", func(p *Params) { p.Policy = "bogus" }},
		{"zero deterministic runners", func(p *Params) { p.DeterministicAdditionalRunners = 0 }},
		{"zero max replications", func(p *Params) { p.MaxReplications = 0 }},
		{"zero max runners per round", func(p *Params) { p.MaxRunnersPerRound = 0 }},
		{"negative max replications", func(p *Params) { p.MaxReplications = -5 }},
	}
	for _, tc := range cases {
		p := valid
		tc.mut(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("%s: invalid params accepted", tc.name)
		}
	}
}

func TestEvaluateInputValidation(t *testing.T) {
	base := func(eps float64) Input {
		return Input{
			RunnerRound:      1,
			NumberOfReps:     1,
			ConfidenceMetric: eps,
			Observations:     []float64{1},
			Params:           testParams(),
		}
	}
	if _, err := Evaluate(base(0)); err == nil {
		t.Fatal("epsilon 0 accepted")
	}
	if _, err := Evaluate(base(-1)); err == nil {
		t.Fatal("negative epsilon accepted")
	}
	if _, err := Evaluate(base(math.NaN())); err == nil {
		t.Fatal("NaN epsilon accepted")
	}
	in := base(0.5)
	in.RunnerRound = 0
	if _, err := Evaluate(in); err == nil {
		t.Fatal("round 0 accepted")
	}
	in = base(0.5)
	in.NumberOfReps = 0
	if _, err := Evaluate(in); err == nil {
		t.Fatal("reps 0 accepted")
	}
	in = base(0.5)
	in.Observations = []float64{1, math.Inf(1)}
	if _, err := Evaluate(in); err == nil {
		t.Fatal("Inf observation accepted")
	}
	in = base(0.5)
	in.Observations = []float64{math.NaN()}
	if _, err := Evaluate(in); err == nil {
		t.Fatal("NaN observation accepted")
	}
}
