// storer.go implements arangoStorer, which satisfies the go-git
// storage.Storer interface. All git state — objects (Blob, Tree, Commit, Tag),
// references (HEAD via Repository, Branch, Tag entities), and internal state
// (config, index, shallow via GitInternalState entities) — is stored via
// [entitygraph.DataManager]. No raw ArangoDB collection references remain after
// GIT-015d.
package arangodb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
)

// arangoStorer implements storage.Storer backed exclusively by
// entitygraph.DataManager. Each instance is scoped to a single agencyID.
//   - git objects (Blob, Tree, Commit, Tag)  → dm, TypeID = object type name
//   - refs (HEAD, branches, tags)             → dm, Repository / Branch / Tag entities
//   - internal state (config, index, shallow) → dm, GitInternalState entities
type arangoStorer struct {
	dm       entitygraph.DataManager
	agencyID string
}

// newArangoStorer constructs a storer scoped to agencyID.
func newArangoStorer(dm entitygraph.DataManager, agencyID string) *arangoStorer {
	return &arangoStorer{dm: dm, agencyID: agencyID}
}

// ── EncodedObjectStorer (via entitygraph.DataManager) ────────────────────────

// gitObjectTypeIDs lists the entitygraph TypeIDs that map to git objects.
// Order is arbitrary; used for AnyObject searches.
var gitObjectTypeIDs = []string{"Blob", "Tree", "Commit", "Tag"}

// typeIDForObject maps a go-git object type to its entitygraph TypeID.
func typeIDForObject(t plumbing.ObjectType) string {
	switch t {
	case plumbing.BlobObject:
		return "Blob"
	case plumbing.TreeObject:
		return "Tree"
	case plumbing.CommitObject:
		return "Commit"
	case plumbing.TagObject:
		return "Tag"
	default:
		return "Blob"
	}
}

// plumbingTypeFromTypeID maps an entitygraph TypeID back to a go-git object type.
func plumbingTypeFromTypeID(typeID string) plumbing.ObjectType {
	switch typeID {
	case "Blob":
		return plumbing.BlobObject
	case "Tree":
		return plumbing.TreeObject
	case "Commit":
		return plumbing.CommitObject
	case "Tag":
		return plumbing.TagObject
	default:
		return plumbing.AnyObject
	}
}

// int64StorerProp extracts an int64 from an entitygraph properties map.
func int64StorerProp(props map[string]any, key string) int64 {
	if v, ok := props[key]; ok {
		switch vv := v.(type) {
		case int64:
			return vv
		case int:
			return int64(vv)
		case float64:
			return int64(vv)
		}
	}
	return 0
}

// decodeEntityToObject reconstructs a plumbing.EncodedObject from the raw bytes
// stored in an entitygraph entity's "data" property.
func decodeEntityToObject(e entitygraph.Entity) (plumbing.EncodedObject, error) {
	dataRaw, ok := e.Properties["data"]
	if !ok {
		return nil, fmt.Errorf("entity %s has no data property", e.ID)
	}
	// dataStr is "" for an empty blob (e.g. e69de29b…) — that is valid.
	dataStr, _ := dataRaw.(string)
	raw, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("decodeEntityToObject %s: base64: %w", e.ID, err)
	}
	objType := plumbingTypeFromTypeID(e.TypeID)
	size := int64StorerProp(e.Properties, "size")
	obj := &plumbing.MemoryObject{}
	obj.SetType(objType)
	obj.SetSize(size)
	if _, err := obj.Write(raw); err != nil {
		return nil, fmt.Errorf("decodeEntityToObject %s: write: %w", e.ID, err)
	}
	return obj, nil
}

// NewEncodedObject returns a new in-memory encoded object.
func (s *arangoStorer) NewEncodedObject() plumbing.EncodedObject {
	return &plumbing.MemoryObject{}
}

// SetEncodedObject reads raw bytes from obj, base64-encodes them, and creates
// an entitygraph entity of the matching TypeID. Idempotent: if an entity with
// the same sha already exists for this agency, the call is a no-op.
func (s *arangoStorer) SetEncodedObject(obj plumbing.EncodedObject) (plumbing.Hash, error) {
	ctx := context.Background()
	hash := obj.Hash()
	typeID := typeIDForObject(obj.Type())
	log.Printf("[DEBUG] SetEncodedObject agency=%s type=%s sha=%s", s.agencyID, typeID, hash)

	// Idempotent: skip if already stored.
	existing, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     typeID,
		Properties: map[string]any{"sha": hash.String()},
	})
	if err == nil && len(existing) > 0 {
		log.Printf("[DEBUG] SetEncodedObject agency=%s type=%s sha=%s: already exists (skip)", s.agencyID, typeID, hash)
		return hash, nil
	}
	if err != nil {
		log.Printf("[DEBUG] SetEncodedObject agency=%s type=%s sha=%s: idempotency check error: %v", s.agencyID, typeID, hash, err)
	}
	r, err := obj.Reader()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: reader: %w", err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: read: %w", err)
	}

	_, createErr := s.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: s.agencyID,
		TypeID:   typeID,
		Properties: map[string]any{
			"sha":  hash.String(),
			"data": base64.StdEncoding.EncodeToString(raw),
			"size": obj.Size(),
		},
	})
	if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
		log.Printf("[DEBUG] SetEncodedObject agency=%s type=%s sha=%s: create failed: %v", s.agencyID, typeID, hash, createErr)
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: create %s: %w", typeID, createErr)
	}
	log.Printf("[DEBUG] SetEncodedObject agency=%s type=%s sha=%s: stored OK", s.agencyID, typeID, hash)

	return hash, nil
}

// EncodedObject retrieves a git object by type and hash from entitygraph.
// Returns plumbing.ErrObjectNotFound when the object is absent.
func (s *arangoStorer) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	ctx := context.Background()
	sha := h.String()
	log.Printf("[DEBUG] EncodedObject agency=%s type=%v sha=%s", s.agencyID, t, sha)

	search := func(typeID string) (plumbing.EncodedObject, error) {
		list, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     typeID,
			Properties: map[string]any{"sha": sha},
		})
		if err != nil {
			log.Printf("[DEBUG] EncodedObject agency=%s type=%s sha=%s: ListEntities error: %v", s.agencyID, typeID, sha, err)
			return nil, plumbing.ErrObjectNotFound
		}
		if len(list) == 0 {
			log.Printf("[DEBUG] EncodedObject agency=%s type=%s sha=%s: not found in ArangoDB", s.agencyID, typeID, sha)
			return nil, plumbing.ErrObjectNotFound
		}
		obj, decErr := decodeEntityToObject(list[0])
		if decErr != nil {
			log.Printf("[DEBUG] EncodedObject agency=%s type=%s sha=%s: decode error: %v", s.agencyID, typeID, sha, decErr)
			return nil, plumbing.ErrObjectNotFound
		}
		log.Printf("[DEBUG] EncodedObject agency=%s type=%s sha=%s: found OK", s.agencyID, typeID, sha)
		return obj, nil
	}

	if t != plumbing.AnyObject {
		return search(typeIDForObject(t))
	}
	for _, typeID := range gitObjectTypeIDs {
		if obj, err := search(typeID); err == nil {
			return obj, nil
		}
	}
	return nil, plumbing.ErrObjectNotFound
}

// IterEncodedObjects returns an iterator over all objects of the given type
// belonging to this agency. Pass plumbing.AnyObject to iterate all types.
func (s *arangoStorer) IterEncodedObjects(t plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	ctx := context.Background()

	collect := func(typeID string) ([]plumbing.EncodedObject, error) {
		list, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID: s.agencyID,
			TypeID:   typeID,
		})
		if err != nil {
			return nil, fmt.Errorf("IterEncodedObjects %s: list: %w", typeID, err)
		}
		objs := make([]plumbing.EncodedObject, 0, len(list))
		for _, e := range list {
			obj, err := decodeEntityToObject(e)
			if err != nil {
				continue // skip entities without raw data (e.g. created by GitManager layer)
			}
			objs = append(objs, obj)
		}
		return objs, nil
	}

	if t != plumbing.AnyObject {
		objs, err := collect(typeIDForObject(t))
		if err != nil {
			return nil, err
		}
		return storer.NewEncodedObjectSliceIter(objs), nil
	}
	var all []plumbing.EncodedObject
	for _, typeID := range gitObjectTypeIDs {
		objs, err := collect(typeID)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	return storer.NewEncodedObjectSliceIter(all), nil
}

// HasEncodedObject returns nil if the object exists, plumbing.ErrObjectNotFound otherwise.
func (s *arangoStorer) HasEncodedObject(h plumbing.Hash) error {
	ctx := context.Background()
	sha := h.String()
	for _, typeID := range gitObjectTypeIDs {
		list, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     typeID,
			Properties: map[string]any{"sha": sha},
		})
		if err == nil && len(list) > 0 {
			log.Printf("[DEBUG] HasEncodedObject agency=%s sha=%s: found (type=%s)", s.agencyID, sha, typeID)
			return nil
		}
	}
	log.Printf("[DEBUG] HasEncodedObject agency=%s sha=%s: NOT FOUND", s.agencyID, sha)
	return plumbing.ErrObjectNotFound
}

// EncodedObjectSize returns the byte size of the raw object data.
func (s *arangoStorer) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	ctx := context.Background()
	sha := h.String()
	for _, typeID := range gitObjectTypeIDs {
		list, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     typeID,
			Properties: map[string]any{"sha": sha},
		})
		if err == nil && len(list) > 0 {
			return int64StorerProp(list[0].Properties, "size"), nil
		}
	}
	return 0, plumbing.ErrObjectNotFound
}

// AddAlternate is a no-op; alternates are not needed for the entitygraph store.
func (s *arangoStorer) AddAlternate(_ string) error { return nil }

// ── ReferenceStorer ───────────────────────────────────────────────────────────

// SetReference upserts a git reference via entitygraph entities.
//
//   - HEAD (symbolic) → updates Repository.head_ref with the symbolic target.
//   - HEAD (hash / detached) → updates Repository.head_ref with the hash string.
//   - refs/heads/<name> → updates Branch.sha with the commit hash.
//   - refs/tags/<name> → no-op: tag sha is set at creation by the gRPC layer;
//     Tag entities are immutable in entitygraph.
func (s *arangoStorer) SetReference(ref *plumbing.Reference) error {
	ctx := context.Background()
	name := ref.Name()
	switch {
	case name == plumbing.HEAD:
		target := ref.Target().String()
		if ref.Type() == plumbing.HashReference {
			target = ref.Hash().String()
		}
		return s.setRepositoryHeadRef(ctx, target)
	case strings.HasPrefix(name.String(), "refs/heads/"):
		branchName := strings.TrimPrefix(name.String(), "refs/heads/")
		return s.setBranchSHA(ctx, branchName, ref.Hash().String())
	default:
		// refs/tags/* and any other refs: no-op.
		return nil
	}
}

// CheckAndSetReference atomically updates a branch reference.
// If old is non-nil, the stored Branch.sha must match old.Hash(); otherwise
// [storage.ErrReferenceHasChanged] is returned.
// Non-branch refs (HEAD, tags) fall through to SetReference without CAS.
func (s *arangoStorer) CheckAndSetReference(new, old *plumbing.Reference) error {
	if old == nil {
		return s.SetReference(new)
	}
	if !strings.HasPrefix(old.Name().String(), "refs/heads/") {
		return s.SetReference(new)
	}
	ctx := context.Background()
	branchName := strings.TrimPrefix(old.Name().String(), "refs/heads/")
	branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "Branch",
		Properties: map[string]any{"name": branchName},
	})
	if err != nil || len(branches) == 0 {
		return fmt.Errorf("CheckAndSetReference: branch %q not found", branchName)
	}
	currentSHA, _ := branches[0].Properties["sha"].(string)
	if currentSHA != old.Hash().String() {
		return storage.ErrReferenceHasChanged
	}
	return s.SetReference(new)
}

// Reference returns the reference with the given name.
// Returns [plumbing.ErrReferenceNotFound] when absent.
func (s *arangoStorer) Reference(name plumbing.ReferenceName) (*plumbing.Reference, error) {
	ctx := context.Background()

	// HEAD — read head_ref from the Repository entity.
	if name == plumbing.HEAD {
		repos, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID: s.agencyID,
			TypeID:   "Repository",
		})
		if err != nil || len(repos) == 0 {
			return nil, plumbing.ErrReferenceNotFound
		}
		headRef, _ := repos[0].Properties["head_ref"].(string)
		if headRef == "" {
			return nil, plumbing.ErrReferenceNotFound
		}
		return plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.ReferenceName(headRef)), nil
	}

	// refs/heads/<name> — read sha from the Branch entity.
	if strings.HasPrefix(name.String(), "refs/heads/") {
		branchName := strings.TrimPrefix(name.String(), "refs/heads/")
		branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     "Branch",
			Properties: map[string]any{"name": branchName},
		})
		if err != nil || len(branches) == 0 {
			return nil, plumbing.ErrReferenceNotFound
		}
		sha, _ := branches[0].Properties["sha"].(string)
		// A branch with no commits (empty or zero SHA) is treated as
		// non-existent so that referenceExists returns false and go-git
		// allows a Create-action push to succeed on the first push.
		if sha == "" || sha == plumbing.ZeroHash.String() {
			return nil, plumbing.ErrReferenceNotFound
		}
		return plumbing.NewHashReference(name, plumbing.NewHash(sha)), nil
	}

	// refs/tags/<name> — read sha from the Tag entity.
	if strings.HasPrefix(name.String(), "refs/tags/") {
		tagName := strings.TrimPrefix(name.String(), "refs/tags/")
		tags, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     "Tag",
			Properties: map[string]any{"name": tagName},
		})
		if err != nil || len(tags) == 0 {
			return nil, plumbing.ErrReferenceNotFound
		}
		sha, _ := tags[0].Properties["sha"].(string)
		return plumbing.NewHashReference(name, plumbing.NewHash(sha)), nil
	}

	return nil, plumbing.ErrReferenceNotFound
}

// IterReferences returns an iterator over all references for this agency:
// HEAD (from Repository.head_ref), all Branch refs (refs/heads/*), and all
// Tag refs (refs/tags/*).
func (s *arangoStorer) IterReferences() (storer.ReferenceIter, error) {
	ctx := context.Background()
	var refs []*plumbing.Reference

	// HEAD from Repository.head_ref.
	repos, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Repository",
	})
	if err == nil && len(repos) > 0 {
		headRef, _ := repos[0].Properties["head_ref"].(string)
		if headRef != "" {
			refs = append(refs, plumbing.NewSymbolicReference(
				plumbing.HEAD, plumbing.ReferenceName(headRef),
			))
		}
	}

	// Branch entities → refs/heads/<name>.
	branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Branch",
	})
	if err != nil {
		return nil, fmt.Errorf("IterReferences: list branches: %w", err)
	}
	for _, b := range branches {
		bname, _ := b.Properties["name"].(string)
		sha, _ := b.Properties["sha"].(string)
		// Skip branches with no commits yet (zero/empty SHA). Advertising
		// a zero-SHA ref causes go-git's server to reject the first push
		// with ErrUpdateReference (Create + exists == true conflict).
		if bname == "" || sha == "" || sha == plumbing.ZeroHash.String() {
			continue
		}
		refs = append(refs, plumbing.NewHashReference(
			plumbing.ReferenceName("refs/heads/"+bname),
			plumbing.NewHash(sha),
		))
	}

	// Tag entities → refs/tags/<name>.
	tags, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Tag",
	})
	if err != nil {
		return nil, fmt.Errorf("IterReferences: list tags: %w", err)
	}
	for _, t := range tags {
		tname, _ := t.Properties["name"].(string)
		sha, _ := t.Properties["sha"].(string)
		if tname == "" {
			continue
		}
		refs = append(refs, plumbing.NewHashReference(
			plumbing.ReferenceName("refs/tags/"+tname),
			plumbing.NewHash(sha),
		))
	}

	return storer.NewReferenceSliceIter(refs), nil
}

// RemoveReference soft-deletes the entity backing the given ref.
// HEAD removal is a no-op. Unknown ref prefixes are silently ignored.
func (s *arangoStorer) RemoveReference(name plumbing.ReferenceName) error {
	ctx := context.Background()

	if strings.HasPrefix(name.String(), "refs/heads/") {
		branchName := strings.TrimPrefix(name.String(), "refs/heads/")
		branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     "Branch",
			Properties: map[string]any{"name": branchName},
		})
		if err != nil || len(branches) == 0 {
			return nil // already absent — no-op
		}
		return s.dm.DeleteEntity(ctx, s.agencyID, branches[0].ID)
	}

	if strings.HasPrefix(name.String(), "refs/tags/") {
		tagName := strings.TrimPrefix(name.String(), "refs/tags/")
		tags, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     "Tag",
			Properties: map[string]any{"name": tagName},
		})
		if err != nil || len(tags) == 0 {
			return nil // already absent — no-op
		}
		return s.dm.DeleteEntity(ctx, s.agencyID, tags[0].ID)
	}

	return nil // HEAD and other refs: no-op
}

// CountLooseRefs returns the combined count of Branch and Tag entities for
// this agency. ArangoDB has no concept of packed vs loose refs.
func (s *arangoStorer) CountLooseRefs() (int, error) {
	ctx := context.Background()
	branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Branch",
	})
	if err != nil {
		return 0, fmt.Errorf("CountLooseRefs: list branches: %w", err)
	}
	tags, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Tag",
	})
	if err != nil {
		return 0, fmt.Errorf("CountLooseRefs: list tags: %w", err)
	}
	return len(branches) + len(tags), nil
}

// PackRefs is a no-op; ArangoDB has no concept of packed vs loose references.
func (s *arangoStorer) PackRefs() error { return nil }

// ── ConfigStorer ──────────────────────────────────────────────────────────────

// Config retrieves the per-repo git config from a GitInternalState entity
// (state_type="config"). Returns an empty config when none is stored.
func (s *arangoStorer) Config() (*gogitconfig.Config, error) {
	ctx := context.Background()
	entities, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "GitInternalState",
		Properties: map[string]any{"state_type": "config"},
	})
	if err != nil || len(entities) == 0 {
		return gogitconfig.NewConfig(), nil
	}
	dataStr, _ := entities[0].Properties["data"].(string)
	if dataStr == "" {
		return gogitconfig.NewConfig(), nil
	}
	raw, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("Config: base64 decode: %w", err)
	}
	cfg := gogitconfig.NewConfig()
	if err := cfg.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("Config: unmarshal: %w", err)
	}
	return cfg, nil
}

// SetConfig serialises the git config and upserts a GitInternalState entity
// with state_type="config".
func (s *arangoStorer) SetConfig(cfg *gogitconfig.Config) error {
	ctx := context.Background()
	raw, err := cfg.Marshal()
	if err != nil {
		return fmt.Errorf("SetConfig: marshal: %w", err)
	}
	_, err = s.dm.UpsertEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: s.agencyID,
		TypeID:   "GitInternalState",
		Properties: map[string]any{
			"state_type": "config",
			"data":       base64.StdEncoding.EncodeToString(raw),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("SetConfig: upsert: %w", err)
	}
	return nil
}

// ── IndexStorer ───────────────────────────────────────────────────────────────

// Index retrieves the staging-area index from a GitInternalState entity
// (state_type="index"). Returns an empty index when none is stored.
func (s *arangoStorer) Index() (*index.Index, error) {
	ctx := context.Background()
	entities, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "GitInternalState",
		Properties: map[string]any{"state_type": "index"},
	})
	if err != nil || len(entities) == 0 {
		return &index.Index{}, nil
	}
	dataStr, _ := entities[0].Properties["data"].(string)
	if dataStr == "" {
		return &index.Index{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("Index: base64 decode: %w", err)
	}
	idx := &index.Index{}
	if err := index.NewDecoder(bytes.NewReader(raw)).Decode(idx); err != nil {
		return nil, fmt.Errorf("Index: decode: %w", err)
	}
	return idx, nil
}

// SetIndex serialises the staging-area index and upserts a GitInternalState
// entity with state_type="index".
func (s *arangoStorer) SetIndex(idx *index.Index) error {
	ctx := context.Background()
	var buf bytes.Buffer
	if err := index.NewEncoder(&buf).Encode(idx); err != nil {
		return fmt.Errorf("SetIndex: encode: %w", err)
	}
	_, err := s.dm.UpsertEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: s.agencyID,
		TypeID:   "GitInternalState",
		Properties: map[string]any{
			"state_type": "index",
			"data":       base64.StdEncoding.EncodeToString(buf.Bytes()),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("SetIndex: upsert: %w", err)
	}
	return nil
}

// ── ShallowStorer ─────────────────────────────────────────────────────────────

// SetShallow persists the list of shallow-clone commit hashes as a
// JSON-encoded, base64-wrapped payload in a GitInternalState entity
// (state_type="shallow").
func (s *arangoStorer) SetShallow(hashes []plumbing.Hash) error {
	ctx := context.Background()
	strs := make([]string, len(hashes))
	for i, h := range hashes {
		strs[i] = h.String()
	}
	raw, err := json.Marshal(strs)
	if err != nil {
		return fmt.Errorf("SetShallow: marshal: %w", err)
	}
	_, err = s.dm.UpsertEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: s.agencyID,
		TypeID:   "GitInternalState",
		Properties: map[string]any{
			"state_type": "shallow",
			"data":       base64.StdEncoding.EncodeToString(raw),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("SetShallow: upsert: %w", err)
	}
	return nil
}

// Shallow returns the stored shallow-clone hash list. Returns nil when absent.
func (s *arangoStorer) Shallow() ([]plumbing.Hash, error) {
	ctx := context.Background()
	entities, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "GitInternalState",
		Properties: map[string]any{"state_type": "shallow"},
	})
	if err != nil || len(entities) == 0 {
		return nil, nil
	}
	dataStr, _ := entities[0].Properties["data"].(string)
	if dataStr == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("Shallow: base64 decode: %w", err)
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err != nil {
		return nil, fmt.Errorf("Shallow: json decode: %w", err)
	}
	hashes := make([]plumbing.Hash, len(strs))
	for i, h := range strs {
		hashes[i] = plumbing.NewHash(h)
	}
	return hashes, nil
}

// ── ModuleStorer ──────────────────────────────────────────────────────────────

// Module returns a sub-storer scoped to a git submodule. The submodule storer
// shares the same DataManager but uses a namespace-prefixed agencyID so its
// objects, refs, and internal state do not collide with the parent repo.
func (s *arangoStorer) Module(name string) (storage.Storer, error) {
	return newArangoStorer(s.dm, s.agencyID+"/module/"+name), nil
}

// ── Blob metadata backfill (single commit point) ──────────────────────────────

// backfillBlobsFromSHA looks up the Commit with the given SHA from the store
// (at this point every packfile object is guaranteed to be present) and walks
// its full tree, calling backfillBlobEntity for every blob entry found.
// It is the single point where name/path/extension are resolved for blobs
// written via the git-push path (SetEncodedObject).
func (s *arangoStorer) backfillBlobsFromSHA(ctx context.Context, commitSHA string) {
	obj, err := s.EncodedObject(plumbing.CommitObject, plumbing.NewHash(commitSHA))
	if err != nil {
		log.Printf("[DEBUG] backfillBlobsFromSHA agency=%s sha=%s: get commit: %v", s.agencyID, commitSHA, err)
		return
	}
	commit := &object.Commit{}
	if err := commit.Decode(obj); err != nil {
		log.Printf("[DEBUG] backfillBlobsFromSHA agency=%s sha=%s: decode commit: %v", s.agencyID, commitSHA, err)
		return
	}
	s.backfillBlobsFromTree(ctx, "", commit.TreeHash)
}

// backfillBlobsFromTree recursively walks a tree object. For each blob entry
// it calls backfillBlobEntity with the fully-qualified path. For subtree
// entries it recurses with the directory prefix extended.
func (s *arangoStorer) backfillBlobsFromTree(ctx context.Context, prefix string, treeHash plumbing.Hash) {
	treeObj, err := s.EncodedObject(plumbing.TreeObject, treeHash)
	if err != nil {
		log.Printf("[DEBUG] backfillBlobsFromTree agency=%s tree=%s: get: %v", s.agencyID, treeHash, err)
		return
	}
	tree := &object.Tree{}
	if err := tree.Decode(treeObj); err != nil {
		log.Printf("[DEBUG] backfillBlobsFromTree agency=%s tree=%s: decode: %v", s.agencyID, treeHash, err)
		return
	}
	for _, entry := range tree.Entries {
		entryPath := entry.Name
		if prefix != "" {
			entryPath = prefix + "/" + entry.Name
		}
		if entry.Mode == filemode.Dir || entry.Mode == filemode.Submodule {
			s.backfillBlobsFromTree(ctx, entryPath, entry.Hash)
			continue
		}
		s.backfillBlobEntity(ctx, entry.Hash.String(), entry.Name, entryPath)
	}
}

// backfillBlobEntity patches the name, path, and extension properties on the
// Blob entity identified by sha, but only when name is not already set.
// This makes the call idempotent: blobs written by WriteFile already carry
// the fields and are left unchanged.
func (s *arangoStorer) backfillBlobEntity(ctx context.Context, sha, name, path string) {
	blobs, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "Blob",
		Properties: map[string]any{"sha": sha},
	})
	if err != nil || len(blobs) == 0 {
		log.Printf("[DEBUG] backfillBlobEntity agency=%s sha=%s: not found", s.agencyID, sha)
		return
	}
	// Idempotent: skip if already enriched (written by the WriteFile path).
	if existing, ok := blobs[0].Properties["name"].(string); ok && existing != "" {
		log.Printf("[DEBUG] backfillBlobEntity agency=%s sha=%s: already enriched, skip", s.agencyID, sha)
		return
	}
	ext := ""
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		ext = name[dot+1:]
	}
	if _, err := s.dm.UpdateEntity(ctx, s.agencyID, blobs[0].ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"name":      name,
			"path":      path,
			"extension": ext,
		},
	}); err != nil {
		log.Printf("[DEBUG] backfillBlobEntity agency=%s sha=%s: update failed: %v", s.agencyID, sha, err)
	}
	log.Printf("[DEBUG] backfillBlobEntity agency=%s sha=%s name=%s path=%s: enriched OK", s.agencyID, sha, name, path)
}

// ── Private helpers ───────────────────────────────────────────────────────────

// setRepositoryHeadRef updates the head_ref property on the Repository entity
// for this agency to the given target (e.g. "refs/heads/main").
func (s *arangoStorer) setRepositoryHeadRef(ctx context.Context, target string) error {
	repos, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: s.agencyID,
		TypeID:   "Repository",
	})
	if err != nil {
		return fmt.Errorf("SetReference HEAD: list repositories: %w", err)
	}
	if len(repos) == 0 {
		return fmt.Errorf("SetReference HEAD: no repository for agency %q", s.agencyID)
	}
	_, err = s.dm.UpdateEntity(ctx, s.agencyID, repos[0].ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{"head_ref": target},
	})
	if err != nil {
		return fmt.Errorf("SetReference HEAD: update repository: %w", err)
	}
	return nil
}

// setBranchSHA updates the sha property on the Branch entity identified by
// branchName for this agency. If the Branch entity does not exist yet (e.g.
// a git client is pushing a new branch that was never created via CreateBranch),
// it is created and linked to the Repository entity automatically.
func (s *arangoStorer) setBranchSHA(ctx context.Context, branchName, sha string) error {
	log.Printf("[DEBUG] setBranchSHA agency=%s branch=%s sha=%s: looking up branch entity", s.agencyID, branchName, sha)
	branches, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "Branch",
		Properties: map[string]any{"name": branchName},
	})
	if err != nil {
		return fmt.Errorf("SetReference refs/heads/%s: list branches: %w", branchName, err)
	}
	if len(branches) == 0 {
		// Branch does not exist — create it so that a plain `git push` can
		// introduce a new branch without requiring an explicit CreateBranch call.
		log.Printf("[DEBUG] setBranchSHA agency=%s branch=%s: not found, creating", s.agencyID, branchName)
		repos, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID: s.agencyID,
			TypeID:   "Repository",
		})
		if err != nil {
			return fmt.Errorf("SetReference refs/heads/%s: list repositories: %w", branchName, err)
		}
		if len(repos) == 0 {
			return fmt.Errorf("SetReference refs/heads/%s: no repository for agency %q", branchName, s.agencyID)
		}
		repoID := repos[0].ID
		now := time.Now().UTC().Format(time.RFC3339)
		branchEntity, err := s.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: s.agencyID,
			TypeID:   "Branch",
			Properties: map[string]any{
				"name":       branchName,
				"is_default": false,
				"sha":        sha,
				"created_at": now,
				"updated_at": now,
			},
			Relationships: []entitygraph.EntityRelationshipRequest{
				{Name: "belongs_to_repository", ToID: repoID},
			},
		})
		if err != nil {
			return fmt.Errorf("SetReference refs/heads/%s: create branch: %w", branchName, err)
		}
		if _, relErr := s.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: s.agencyID,
			Name:     "has_branch",
			FromID:   repoID,
			ToID:     branchEntity.ID,
		}); relErr != nil {
			return fmt.Errorf("SetReference refs/heads/%s: link branch to repo: %w", branchName, relErr)
		}
		log.Printf("[DEBUG] setBranchSHA agency=%s branch=%s sha=%s: created OK", s.agencyID, branchName, sha)

		// All packfile objects are now stored — backfill blob metadata.
		s.backfillBlobsFromSHA(ctx, sha)
		return nil
	}
	_, err = s.dm.UpdateEntity(ctx, s.agencyID, branches[0].ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{"sha": sha},
	})
	if err != nil {
		return fmt.Errorf("SetReference refs/heads/%s: update branch: %w", branchName, err)
	}

	// All packfile objects are now in the store — backfill blob metadata.
	s.backfillBlobsFromSHA(ctx, sha)
	return nil
}
