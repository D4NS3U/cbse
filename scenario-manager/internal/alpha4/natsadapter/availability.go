package natsadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/messaging"
	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/subject"
	natsgo "github.com/nats-io/nats.go"
)

// EDSAvailabilityResponder answers EDS availability probes on the Core NATS
// request/reply subject cbse.*.*.eds.scenarios.available. It parses the
// subject, validates the identity, fetches the experiment, applies the
// lifecycle gate, and replies ready with the exact batch subject for an
// admitted experiment or error with no batch subject otherwise.
type EDSAvailabilityResponder struct {
	a *Adapters
}

// NewEDSAvailabilityResponder returns a responder bound to the adapter set.
func NewEDSAvailabilityResponder(a *Adapters) *EDSAvailabilityResponder {
	return &EDSAvailabilityResponder{a: a}
}

// Start subscribes to the cluster-wide EDS availability wildcard and replies to
// each probe. It returns an error if the subscription fails. The subscription
// is Core NATS (not JetStream); on shutdown the caller cancels ctx and the
// connection close cleans up the subscription.
func (r *EDSAvailabilityResponder) Start(ctx context.Context) error {
	if r.a == nil || r.a.nc == nil {
		return fmt.Errorf("NATS connection is not initialized")
	}
	if _, err := r.a.nc.QueueSubscribe(subject.EDSAvailabilityWildcard, messaging.EDSConsumerConfig().DeliverGroup, func(msg *natsgo.Msg) {
		reply, err := r.HandleAvailability(ctx, msg.Subject, msg.Data)
		if err != nil {
			log.Printf("alpha4 eds availability: subject=%q: %v", msg.Subject, err)
		}
		respondAvailability(msg, reply)
	}); err != nil {
		return fmt.Errorf("subscribe to EDS availability subject %q: %w", subject.EDSAvailabilityWildcard, err)
	}
	if err := r.a.nc.Flush(); err != nil {
		return fmt.Errorf("flush EDS availability subscription: %w", err)
	}
	return nil
}

// HandleAvailability applies the availability workflow to one probe and
// returns the reply. It is pure with respect to the transport: it takes the raw
// subject and payload and returns the reply, so it can be unit-tested without
// a NATS server.
func (r *EDSAvailabilityResponder) HandleAvailability(ctx context.Context, subjectStr string, data []byte) (communication.EDSAvailabilityReply, error) {
	identity, err := subject.ParseIdentity(subjectStr)
	if err != nil {
		return errorReply("invalid availability subject"), err
	}
	// Decode the (optional) availability payload to surface a malformed request
	// as an error reply; the EDS may send an empty payload.
	var notice communication.EDSAvailabilityRequest
	if len(data) > 0 {
		if err := json.Unmarshal(data, &notice); err != nil {
			return errorReply("invalid availability payload"), fmt.Errorf("decode availability payload: %w", err)
		}
	}
	notice.ProjectNamespace = identity.Namespace.String()
	notice.ProjectName = identity.Project.String()

	decision, err := r.a.admit(ctx, identity.Namespace.String(), identity.Project.String())
	if err != nil {
		// Transient lookup failure: reply error so the EDS retries availability.
		return errorReply("experiment lookup failed"), err
	}
	if !decision.IsAdmitted() {
		// Unavailable (Pending/Provisioning/empty) or terminal: no batch subject.
		return errorReply(fmt.Sprintf("experiment %s", decision)), nil
	}
	return communication.EDSAvailabilityReply{
		Status:       communication.AvailabilityStatusReady,
		BatchSubject: subject.EDSBatchSubject(identity.Namespace, identity.Project),
	}, nil
}

// errorReply returns an availability error reply with no batch subject.
func errorReply(reason string) communication.EDSAvailabilityReply {
	return communication.EDSAvailabilityReply{Status: communication.AvailabilityStatusError, Reason: reason}
}

// respondAvailability marshals and publishes the reply when a reply subject is
// present.
func respondAvailability(msg *natsgo.Msg, reply communication.EDSAvailabilityReply) {
	data, err := json.Marshal(reply)
	if err != nil {
		log.Printf("alpha4 eds availability: marshal reply: %v", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		log.Printf("alpha4 eds availability: publish reply: %v", err)
	}
}
