// storer.go implements arangoStorer, which satisfies the go-git
// storage.Storer interface. Git objects (Blob, Tree, Commit, Tag) are stored
// via [entitygraph.DataManager]; refs, config, index, and shallow state are
// stored in ArangoDB document collections (gitraw_refs, gitraw_config,
// gitraw_index, gitraw_shallow) until those are migrated in GIT-015d.
//
// ArangoDB key constraint: only letters, digits, underscore (_), dash (−), and
// colon (:) are allowed; the first character must be alphanumeric. This file
// sanitises all inputs accordingly.
package arangodb

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	driver "github.com/arangodb/go-driver"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
)

// Collection names for ref, config, index, and shallow state.
// git objects (Blob, Tree, Commit, Tag) are stored in entitygraph (see GIT-015c).
// gitraw_refs, gitraw_config, gitraw_index, gitraw_shallow are migrated to
// entitygraph in GIT-015d.
const (
	colRefs    = "gitraw_refs"
	colConfig  = "gitraw_config"
	colIndex   = "gitraw_index"
	colShallow = "gitraw_shallow"
)

// arangoStorer implements storage.Storer backed by ArangoDB and
// entitygraph.DataManager.
// Each instance is scoped to a single agencyID.
//   - git objects (Blob, Tree, Commit, Tag) → dm (entitygraph.DataManager)
//   - refs, config, index, shallow → db (raw ArangoDB, migrated in GIT-015d)
type arangoStorer struct {
	db       driver.Database         // refs, config, index, shallow (until GIT-015d)
	dm       entitygraph.DataManager // git objects: Blob, Tree, Commit, Tag
	agencyID string
}

// newArangoStorer constructs a storer scoped to agencyID.
func newArangoStorer(db driver.Database, dm entitygraph.DataManager, agencyID string) *arangoStorer {
	return &arangoStorer{db: db, dm: dm, agencyID: agencyID}
}

// ── Key helpers (refs, config, index, shallow) ────────────────────────────────

// safeSegment replaces any character that is not alphanumeric, dash, or colon
// with an underscore so the result is a valid ArangoDB key segment.
func safeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// refKey returns the ArangoDB document key for a git reference.
// Format: {safeAgencyID}:{sanitisedRefName}
// Forward-slashes in ref names (e.g. refs/heads/main) are replaced by '_'.
func (s *arangoStorer) refKey(name plumbing.ReferenceName) string {
	return safeSegment(s.agencyID) + ":" + safeSegment(name.String())
}

// singleKey returns the document key for single-document stores
// (config, index, shallow). Format: {safeAgencyID}
func (s *arangoStorer) singleKey() string {
	return safeSegment(s.agencyID)
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
	dataStr, _ := e.Properties["data"].(string)
	if dataStr == "" {
		return nil, fmt.Errorf("entity %s has no data property", e.ID)
	}
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

	// Idempotent: skip if already stored.
	existing, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     typeID,
		Properties: map[string]any{"sha": hash.String()},
	})
	if err == nil && len(existing) > 0 {
		return hash, nil
	}

	// Read raw bytes from the encoded object.
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
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: create %s: %w", typeID, createErr)
	}
	return hash, nil
}

// EncodedObject retrieves a git object by type and hash from entitygraph.
// Returns plumbing.ErrObjectNotFound when the object is absent.
func (s *arangoStorer) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	ctx := context.Background()
	sha := h.String()

	search := func(typeID string) (plumbing.EncodedObject, error) {
		list, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     typeID,
			Properties: map[string]any{"sha": sha},
		})
		if err != nil || len(list) == 0 {
			return nil, plumbing.ErrObjectNotFound
		}
		return decodeEntityToObject(list[0])
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
			return nil
		}
	}
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

// refDoc is the ArangoDB document shape for gitraw_refs.
type refDoc struct {
	Key      string `json:"_key,omitempty"`
	AgencyID string `json:"agencyID"`
	RefName  string `json:"refName"`
	Target   string `json:"target,omitempty"`   // direct ref hash
	Symbolic string `json:"symbolic,omitempty"` // symbolic ref target
}

// SetReference upserts a reference document. Creates if absent; replaces if present.
func (s *arangoStorer) SetReference(ref *plumbing.Reference) error {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colRefs)
	if err != nil {
		return fmt.Errorf("SetReference: collection: %w", err)
	}
	doc := s.refToDoc(ref)
	if _, createErr := col.CreateDocument(ctx, doc); driver.IsConflict(createErr) {
		_, updateErr := col.UpdateDocument(ctx, doc.Key, doc)
		return updateErr
	} else {
		return createErr
	}
}

// CheckAndSetReference atomically updates a reference. If old is non-nil, the
// current stored hash must match old.Hash() — otherwise an error is returned.
func (s *arangoStorer) CheckAndSetReference(new, old *plumbing.Reference) error {
	if old == nil {
		return s.SetReference(new)
	}
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colRefs)
	if err != nil {
		return fmt.Errorf("CheckAndSetReference: collection: %w", err)
	}
	var existing refDoc
	if _, err = col.ReadDocument(ctx, s.refKey(old.Name()), &existing); err != nil {
		if driver.IsNotFound(err) {
			return fmt.Errorf("CheckAndSetReference: reference %q not found", old.Name())
		}
		return fmt.Errorf("CheckAndSetReference: read: %w", err)
	}
	if existing.Target != old.Hash().String() {
		return fmt.Errorf("CheckAndSetReference: reference %q has changed (expected %s got %s)",
			old.Name(), old.Hash(), existing.Target)
	}
	return s.SetReference(new)
}

// Reference returns the reference with the given name.
// Returns plumbing.ErrReferenceNotFound when absent.
func (s *arangoStorer) Reference(name plumbing.ReferenceName) (*plumbing.Reference, error) {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colRefs)
	if err != nil {
		return nil, fmt.Errorf("Reference: collection: %w", err)
	}
	var doc refDoc
	if _, err = col.ReadDocument(ctx, s.refKey(name), &doc); err != nil {
		if driver.IsNotFound(err) {
			return nil, plumbing.ErrReferenceNotFound
		}
		return nil, fmt.Errorf("Reference: read: %w", err)
	}
	return s.docToRef(doc), nil
}

// IterReferences returns an iterator over all references for this agency.
func (s *arangoStorer) IterReferences() (storer.ReferenceIter, error) {
	ctx := context.Background()
	query := "FOR doc IN " + colRefs + " FILTER doc.agencyID == @a RETURN doc"
	cursor, err := s.db.Query(ctx, query, map[string]any{"a": s.agencyID})
	if err != nil {
		return nil, fmt.Errorf("IterReferences: query: %w", err)
	}
	defer cursor.Close()
	var refs []*plumbing.Reference
	for cursor.HasMore() {
		var doc refDoc
		if _, err := cursor.ReadDocument(ctx, &doc); err != nil {
			return nil, fmt.Errorf("IterReferences: read: %w", err)
		}
		refs = append(refs, s.docToRef(doc))
	}
	return storer.NewReferenceSliceIter(refs), nil
}

// RemoveReference deletes a reference by name. No-op if already absent.
func (s *arangoStorer) RemoveReference(name plumbing.ReferenceName) error {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colRefs)
	if err != nil {
		return fmt.Errorf("RemoveReference: collection: %w", err)
	}
	if _, err = col.RemoveDocument(ctx, s.refKey(name)); err != nil && !driver.IsNotFound(err) {
		return fmt.Errorf("RemoveReference: %w", err)
	}
	return nil
}

// CountLooseRefs returns the number of references for this agency.
func (s *arangoStorer) CountLooseRefs() (int, error) {
	ctx := context.Background()
	query := "RETURN LENGTH(FOR doc IN " + colRefs + " FILTER doc.agencyID == @a RETURN 1)"
	cursor, err := s.db.Query(ctx, query, map[string]any{"a": s.agencyID})
	if err != nil {
		return 0, fmt.Errorf("CountLooseRefs: query: %w", err)
	}
	defer cursor.Close()
	var count int
	if cursor.HasMore() {
		if _, err := cursor.ReadDocument(ctx, &count); err != nil {
			return 0, fmt.Errorf("CountLooseRefs: read: %w", err)
		}
	}
	return count, nil
}

// PackRefs is a no-op; ArangoDB has no concept of packed vs loose references.
func (s *arangoStorer) PackRefs() error { return nil }

// ── ConfigStorer ──────────────────────────────────────────────────────────────

// Config retrieves the per-repo git config. Returns an empty config when none is stored.
func (s *arangoStorer) Config() (*gogitconfig.Config, error) {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colConfig)
	if err != nil {
		return gogitconfig.NewConfig(), nil
	}
	var doc struct {
		Data string `json:"data"`
	}
	if _, err = col.ReadDocument(ctx, s.singleKey(), &doc); err != nil {
		if driver.IsNotFound(err) {
			return gogitconfig.NewConfig(), nil
		}
		return nil, fmt.Errorf("Config: read: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("Config: base64 decode: %w", err)
	}
	cfg := gogitconfig.NewConfig()
	if err := cfg.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("Config: unmarshal: %w", err)
	}
	return cfg, nil
}

// SetConfig serialises and upserts the git config document.
func (s *arangoStorer) SetConfig(cfg *gogitconfig.Config) error {
	ctx := context.Background()
	raw, err := cfg.Marshal()
	if err != nil {
		return fmt.Errorf("SetConfig: marshal: %w", err)
	}
	col, err := s.db.Collection(ctx, colConfig)
	if err != nil {
		return fmt.Errorf("SetConfig: collection: %w", err)
	}
	doc := map[string]any{
		"_key":     s.singleKey(),
		"agencyID": s.agencyID,
		"data":     base64.StdEncoding.EncodeToString(raw),
	}
	if _, createErr := col.CreateDocument(ctx, doc); driver.IsConflict(createErr) {
		_, updateErr := col.UpdateDocument(ctx, s.singleKey(), doc)
		return updateErr
	} else {
		return createErr
	}
}

// ── IndexStorer ───────────────────────────────────────────────────────────────

// Index retrieves the staging-area index. Returns an empty index when none is stored.
func (s *arangoStorer) Index() (*index.Index, error) {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colIndex)
	if err != nil {
		return &index.Index{}, nil
	}
	var doc struct {
		Data string `json:"data"`
	}
	if _, err = col.ReadDocument(ctx, s.singleKey(), &doc); err != nil {
		if driver.IsNotFound(err) {
			return &index.Index{}, nil
		}
		return nil, fmt.Errorf("Index: read: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("Index: base64 decode: %w", err)
	}
	idx := &index.Index{}
	if err := index.NewDecoder(bytes.NewReader(raw)).Decode(idx); err != nil {
		return nil, fmt.Errorf("Index: decode: %w", err)
	}
	return idx, nil
}

// SetIndex serialises and upserts the staging-area index document.
func (s *arangoStorer) SetIndex(idx *index.Index) error {
	ctx := context.Background()
	var buf bytes.Buffer
	if err := index.NewEncoder(&buf).Encode(idx); err != nil {
		return fmt.Errorf("SetIndex: encode: %w", err)
	}
	col, err := s.db.Collection(ctx, colIndex)
	if err != nil {
		return fmt.Errorf("SetIndex: collection: %w", err)
	}
	doc := map[string]any{
		"_key":     s.singleKey(),
		"agencyID": s.agencyID,
		"data":     base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
	if _, createErr := col.CreateDocument(ctx, doc); driver.IsConflict(createErr) {
		_, updateErr := col.UpdateDocument(ctx, s.singleKey(), doc)
		return updateErr
	} else {
		return createErr
	}
}

// ── ShallowStorer ─────────────────────────────────────────────────────────────

// SetShallow persists the list of shallow clone commit hashes.
func (s *arangoStorer) SetShallow(hashes []plumbing.Hash) error {
	ctx := context.Background()
	strs := make([]string, len(hashes))
	for i, h := range hashes {
		strs[i] = h.String()
	}
	col, err := s.db.Collection(ctx, colShallow)
	if err != nil {
		return fmt.Errorf("SetShallow: collection: %w", err)
	}
	doc := map[string]any{
		"_key":     s.singleKey(),
		"agencyID": s.agencyID,
		"hashes":   strs,
	}
	if _, createErr := col.CreateDocument(ctx, doc); driver.IsConflict(createErr) {
		_, updateErr := col.UpdateDocument(ctx, s.singleKey(), doc)
		return updateErr
	} else {
		return createErr
	}
}

// Shallow returns the stored shallow clone hash list. Returns nil when absent.
func (s *arangoStorer) Shallow() ([]plumbing.Hash, error) {
	ctx := context.Background()
	col, err := s.db.Collection(ctx, colShallow)
	if err != nil {
		return nil, nil
	}
	var doc struct {
		Hashes []string `json:"hashes"`
	}
	if _, err = col.ReadDocument(ctx, s.singleKey(), &doc); err != nil {
		if driver.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Shallow: read: %w", err)
	}
	hashes := make([]plumbing.Hash, len(doc.Hashes))
	for i, h := range doc.Hashes {
		hashes[i] = plumbing.NewHash(h)
	}
	return hashes, nil
}

// ── ModuleStorer ──────────────────────────────────────────────────────────────

// Module returns a sub-storer scoped to a git submodule.
// The submodule storer shares the same database and DataManager but uses a
// namespace-prefixed agencyID so its objects and refs do not collide with the
// parent repo.
func (s *arangoStorer) Module(name string) (storage.Storer, error) {
	return newArangoStorer(s.db, s.dm, s.agencyID+"/"+name), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// refToDoc converts a plumbing.Reference to its ArangoDB document form.
func (s *arangoStorer) refToDoc(ref *plumbing.Reference) refDoc {
	doc := refDoc{
		Key:      s.refKey(ref.Name()),
		AgencyID: s.agencyID,
		RefName:  ref.Name().String(),
	}
	switch ref.Type() {
	case plumbing.HashReference:
		doc.Target = ref.Hash().String()
	case plumbing.SymbolicReference:
		doc.Symbolic = ref.Target().String()
	}
	return doc
}

// docToRef converts an ArangoDB refDoc to a plumbing.Reference.
func (s *arangoStorer) docToRef(doc refDoc) *plumbing.Reference {
	name := plumbing.ReferenceName(doc.RefName)
	if doc.Symbolic != "" {
		return plumbing.NewSymbolicReference(name, plumbing.ReferenceName(doc.Symbolic))
	}
	return plumbing.NewHashReference(name, plumbing.NewHash(doc.Target))
}
