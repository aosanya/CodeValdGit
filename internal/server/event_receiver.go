package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	case codevaldgit.TopicFileWrite:
		go s.handleFileWrite(context.Background(), req.GetPayload())
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

// handleFileWrite processes a git.file.write event from CodeValdAI.
// It resolves the repository and branch by name, calls WriteFile to create a
// commit in ArangoDB, and publishes git.file.written with the commit SHA so
// CodeValdAI can update the run debrief.
func (s *EventReceiverServer) handleFileWrite(ctx context.Context, rawPayload string) {
	var p codevaldgit.FileWritePayload
	if err := json.Unmarshal([]byte(rawPayload), &p); err != nil {
		log.Printf("codevaldgit: handleFileWrite: unmarshal: %v", err)
		return
	}
	p.Resolve()
	if p.Repository == "" || p.BranchName == "" || p.Path == "" {
		log.Printf("codevaldgit: handleFileWrite: missing repository, branch_name, or path")
		return
	}

	repo, err := s.mgr.GetRepositoryByName(ctx, p.Repository)
	if err != nil {
		log.Printf("codevaldgit: handleFileWrite: GetRepositoryByName %q: %v", p.Repository, err)
		return
	}

	branch, err := s.mgr.GetBranchByName(ctx, repo.ID, p.BranchName)
	if err != nil {
		// Branch does not exist — create it from the repo default branch.
		log.Printf("codevaldgit: handleFileWrite: branch %q not found, creating", p.BranchName)
		branch, err = s.mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
			RepositoryID: repo.ID,
			Name:         p.BranchName,
		})
		if err != nil {
			log.Printf("codevaldgit: handleFileWrite: CreateBranch %q: %v", p.BranchName, err)
			return
		}
	}

	commit, err := s.mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID:    branch.ID,
		Path:        p.Path,
		Content:     p.Content,
		Message:     p.Message,
		AuthorName:  p.AuthorName,
		AuthorEmail: p.AuthorEmail,
	})
	if err != nil {
		log.Printf("codevaldgit: handleFileWrite: WriteFile path=%q branch=%q: %v", p.Path, p.BranchName, err)
		return
	}
	log.Printf("codevaldgit: handleFileWrite: wrote path=%q branch=%q commit=%s", p.Path, p.BranchName, commit.SHA)

	// Tag the blob with any keyword annotations the LLM provided.
	if len(p.Keywords) > 0 {
		s.tagBlob(ctx, branch.ID, p.Path, p.Keywords)
	}

	// Publish confirmation so CodeValdAI can update the run debrief.
	eventbus.SafePublish(ctx, s.pub, eventbus.Event{
		Topic:    codevaldgit.TopicFileWritten,
		AgencyID: s.agencyID,
		Payload: codevaldgit.FileWrittenPayload{
			RunID:      p.RunID,
			Repository: p.Repository,
			BranchName: p.BranchName,
			Path:       p.Path,
			CommitSHA:  commit.SHA,
		},
	})
}

// tagBlob creates or finds Keyword entities from the taxonomy tree and wires
// tagged_with edges to the blob written at path.
//
// For each FileWriteKeyword it:
//  1. Resolves or creates the parent keyword (if Parent is set).
//  2. Resolves or creates the keyword itself under that parent.
//  3. Creates a tagged_with edge carrying the signal depth and note.
func (s *EventReceiverServer) tagBlob(ctx context.Context, branchID, path string, keywords []codevaldgit.FileWriteKeyword) {
	blob, err := s.mgr.ReadFile(ctx, branchID, path)
	if err != nil {
		log.Printf("codevaldgit: tagBlob: ReadFile path=%q: %v", path, err)
		return
	}
	for _, kw := range keywords {
		if kw.Name == "" || kw.Signal == "" {
			continue
		}

		// Step 1: resolve or create the parent keyword.
		var parentID string
		if kw.Parent != "" {
			parent, err := s.findOrCreateKeyword(ctx, kw.Parent, "", "", "")
			if err != nil {
				log.Printf("codevaldgit: tagBlob: parent keyword %q: %v", kw.Parent, err)
				continue
			}
			parentID = parent.ID
		}

		// Step 2: resolve or create the keyword itself.
		keyword, err := s.findOrCreateKeyword(ctx, kw.Name, kw.Description, kw.Scope, parentID)
		if err != nil {
			log.Printf("codevaldgit: tagBlob: keyword %q: %v", kw.Name, err)
			continue
		}

		// Step 3: wire the tagged_with edge on this branch.
		props := map[string]any{"signal": kw.Signal}
		if kw.Note != "" {
			props["note"] = kw.Note
		}
		if err := s.mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
			BranchID:         branchID,
			FromEntityID:     blob.ID,
			ToEntityID:       keyword.ID,
			RelationshipName: "tagged_with",
			Properties:       props,
		}); err != nil {
			log.Printf("codevaldgit: tagBlob: CreateEdge blob=%s keyword=%s: %v", blob.ID, keyword.ID, err)
		}
	}
}

// findOrCreateKeyword returns an existing Keyword by name (under parentID, or
// at root level when parentID is empty), creating it with the given description
// and scope when it does not yet exist.
func (s *EventReceiverServer) findOrCreateKeyword(ctx context.Context, name, description, scope, parentID string) (codevaldgit.Keyword, error) {
	kw, err := s.mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{
		Name:        name,
		Description: description,
		Scope:       scope,
		ParentID:    parentID,
	})
	if err == nil {
		return kw, nil
	}
	if !errors.Is(err, codevaldgit.ErrKeywordAlreadyExists) {
		return codevaldgit.Keyword{}, err
	}

	// Already exists — find it by listing siblings (children of the same parent).
	siblings, err := s.mgr.ListKeywords(ctx, codevaldgit.KeywordFilter{ParentID: parentID})
	if err != nil {
		return codevaldgit.Keyword{}, fmt.Errorf("list keywords parentID=%q: %w", parentID, err)
	}
	for _, k := range siblings {
		if k.Name == name {
			return k, nil
		}
	}
	return codevaldgit.Keyword{}, fmt.Errorf("keyword %q not found after ErrKeywordAlreadyExists", name)
}
