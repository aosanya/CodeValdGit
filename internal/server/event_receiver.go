package server

import (
	"context"
	"encoding/json"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/eventbus"
	sharedev1 "github.com/aosanya/CodeValdSharedLib/gen/go/codevaldshared/v1"
)

// EventReceiverServer implements sharedev1.EventReceiverServiceServer for
// CodeValdGit. Cross calls NotifyEvent to push subscribed events; currently
// the only handled topic is [codevaldgit.TopicBranchCreate], which creates a
// branch and publishes [codevaldgit.TopicBranchFetched] on success.
type EventReceiverServer struct {
	sharedev1.UnimplementedEventReceiverServiceServer
	mgr      codevaldgit.GitManager
	pub      eventbus.Publisher
	agencyID string
}

// NewEventReceiver constructs an EventReceiverServer.
func NewEventReceiver(mgr codevaldgit.GitManager, pub eventbus.Publisher, agencyID string) *EventReceiverServer {
	return &EventReceiverServer{mgr: mgr, pub: pub, agencyID: agencyID}
}

// NotifyEvent receives a pushed event from Cross and dispatches it
// asynchronously so the ACK is returned immediately.
func (s *EventReceiverServer) NotifyEvent(_ context.Context, req *sharedev1.NotifyEventRequest) (*sharedev1.NotifyEventResponse, error) {
	log.Printf("codevaldgit: NotifyEvent: topic=%s source=%s event_id=%s",
		req.GetTopic(), req.GetSource(), req.GetEventId())

	switch req.GetTopic() {
	case codevaldgit.TopicBranchCreate:
		go s.handleBranchCreate(context.Background(), req.GetPayload())
	default:
		log.Printf("codevaldgit: NotifyEvent: unhandled topic=%s", req.GetTopic())
	}

	return &sharedev1.NotifyEventResponse{}, nil
}

func (s *EventReceiverServer) handleBranchCreate(ctx context.Context, rawPayload string) {
	var p codevaldgit.BranchCreatePayload
	if err := json.Unmarshal([]byte(rawPayload), &p); err != nil {
		log.Printf("codevaldgit: handleBranchCreate: unmarshal payload: %v", err)
		return
	}
	p.Resolve()
	if p.Repository == "" || p.Name == "" {
		log.Printf("codevaldgit: handleBranchCreate: missing repository or name in payload")
		return
	}

	repo, err := s.mgr.GetRepositoryByName(ctx, p.Repository)
	if err != nil {
		log.Printf("codevaldgit: handleBranchCreate: GetRepositoryByName %q: %v", p.Repository, err)
		return
	}

	branch, err := s.mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID,
		Name:         p.Name,
	})
	if err != nil {
		log.Printf("codevaldgit: handleBranchCreate: CreateBranch %q in repo %q: %v", p.Name, p.Repository, err)
		return
	}
	log.Printf("codevaldgit: handleBranchCreate: created branch %q (id=%s) in repo %q", branch.Name, branch.ID, p.Repository)

	eventbus.SafePublish(ctx, s.pub, eventbus.Event{
		Topic:    codevaldgit.TopicBranchFetched,
		AgencyID: s.agencyID,
		Payload:  codevaldgit.BranchFetchedPayload{BranchID: branch.ID, RepoID: repo.ID},
	})
}
