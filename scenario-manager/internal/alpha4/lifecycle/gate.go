package lifecycle

import (
	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
)

// AdmitDecision classifies the result of the canonical lifecycle gate.
type AdmitDecision int

const (
	// AdmitUnavailable means the experiment is not yet active (Pending,
	// Provisioning, or empty phase) and not deleting. For availability the
	// caller returns status=error without a batch subject; for a JetStream
	// delivery the caller NAKs for later redelivery. No database mutation,
	// attempt consumption, or Job creation is performed.
	AdmitUnavailable AdmitDecision = iota
	// Admit means the experiment is live, non-deleting, and InProgress. The
	// caller may publish a Translator request or perform domain processing.
	Admit
	// AdmitTerminal means the experiment is permanently inactive for messaging
	// (Error, Failed, Completed, or deletion). For availability the caller
	// returns status=error without a batch subject; for a JetStream delivery
	// the caller ACKs and discards without database mutation, attempt
	// consumption, or Job creation.
	AdmitTerminal
)

// String returns a stable name for logs and tests.
func (d AdmitDecision) String() string {
	switch d {
	case AdmitUnavailable:
		return "unavailable"
	case Admit:
		return "admit"
	case AdmitTerminal:
		return "terminal"
	default:
		return "unknown"
	}
}

// AdmitExperiment applies the canonical lifecycle gate to a fetched experiment.
// It returns Admit only when the object is non-nil, has no DeletionTimestamp,
// and has phase InProgress. Terminal phases (Error, Failed, Completed) and a
// present DeletionTimestamp map to AdmitTerminal; a non-terminal phase is
// terminal when deletion has begun. Pending, Provisioning, and an empty phase
// map to AdmitUnavailable.
//
// A nil object (the lookup found no experiment) maps to AdmitTerminal: the
// experiment is gone or never existed, so a late delivery is ACKed as poison
// and an availability request returns status=error. The caller distinguishes a
// transient lookup failure (retryable) from a permanent absence (terminal)
// based on the lookup error, not this function.
func AdmitExperiment(exp *experimentalpha4.SimulationExperiment) AdmitDecision {
	if exp == nil {
		return AdmitTerminal
	}
	if !exp.DeletionTimestamp.IsZero() {
		return AdmitTerminal
	}
	switch exp.Status.Phase {
	case PhaseInProgress:
		return Admit
	case PhaseError, PhaseFailed, PhaseCompleted:
		return AdmitTerminal
	case PhasePending, PhaseProvisioning, "":
		return AdmitUnavailable
	default:
		// An unknown phase is conservatively terminal so a misconfigured
		// experiment cannot consume work.
		return AdmitTerminal
	}
}

// IsAdmitted reports whether the decision permits scenario work.
func (d AdmitDecision) IsAdmitted() bool { return d == Admit }

// IsTerminal reports whether the decision is a permanent inactivation.
func (d AdmitDecision) IsTerminal() bool { return d == AdmitTerminal }
