// Package arangodb provides an ArangoDB-backed implementation of
// [codevaldgit.Backend]. Git objects (blobs, trees, commits, refs, index,
// config) are stored in ArangoDB collections partitioned by agencyID, so
// repositories survive container restarts without a mounted volume.
//
// The working tree ([billy.Filesystem]) remains on a local or in-memory
// filesystem — only the Git object store moves to ArangoDB.
//
// # Collections
//
// Four shared collections are used, each keyed by "{agencyID}/{key}":
//   - git_objects — blobs, trees, commits and tags (base64-encoded)
//   - git_refs    — branch and tag references (including symbolic refs)
//   - git_index   — staging area (base64-encoded index file)
//   - git_config  — per-repo git config (base64-encoded config file)
//
// # Usage
//
//	db, _ := client.Database(ctx, "_system")
//	b, _ := arangodb.NewArangoBackend(arangodb.ArangoConfig{Database: db})
//	mgr, _ := codevaldgit.NewRepoManager(b)
package arangodb

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	driver "github.com/arangodb/go-driver"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gitindex "github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	gogitstorage "github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ──────────────────────────────────────────────────────────────────────────────
// ArangoConfig + NewArangoBackend
// ──────────────────────────────────────────────────────────────────────────────

// ArangoConfig holds the ArangoDB connection and worktree settings
// for the ArangoDB backend.
type ArangoConfig struct {
	// Endpoint is the ArangoDB server URL (e.g. "http://localhost:8529").
	Endpoint string

	// Database is the ArangoDB database name that holds the four Git collections
	// (git_objects, git_refs, git_index, git_config).
	Database string

	// User is the ArangoDB username.
	User string

	// Password is the ArangoDB password.
	Password string

	// WorktreePath is the local path for the billy.Filesystem working tree.
	// Use "" for an in-memory worktree (memfs) — the recommended default for
	// the ArangoDB backend; committed objects persist in ArangoDB regardless.
	WorktreePath string
}

// arangoBackend implements [codevaldgit.Backend] using ArangoDB collections.
type arangoBackend struct {
	db  driver.Database
	cfg ArangoConfig
}

// NewArangoBackend constructs an ArangoDB-backed [codevaldgit.Backend].
// It opens (or creates) the four Git collections in the provided database.
// Returns an error if the database is nil or collection access fails.
func NewArangoBackend(cfg ArangoConfig) (codevaldgit.Backend, error) {
	if cfg.Database == nil {
		return nil, errors.New("NewArangoBackend: Database must not be nil")
	}
	ctx := context.Background()
	for _, name := range []string{collObjects, collRefs, collIndex, collConfig} {
		if _, err := ensureCollection(ctx, cfg.Database, name); err != nil {
			return nil, fmt.Errorf("NewArangoBackend: ensure collection %q: %w", name, err)
		}
	}
	return &arangoBackend{db: cfg.Database, cfg: cfg}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Collection names
// ──────────────────────────────────────────────────────────────────────────────

const (
	collObjects = "git_objects"
	collRefs    = "git_refs"
	collIndex   = "git_index"
	collConfig  = "git_config"
)

// ensureCollection opens a collection, creating it if it doesn't exist.
func ensureCollection(ctx context.Context, db driver.Database, name string) (driver.Collection, error) {
	col, err := db.Collection(ctx, name)
	if err == nil {
		return col, nil
	}
	if driver.IsNotFound(err) {
		col, err = db.CreateCollection(ctx, name, nil)
		if err != nil && !driver.IsConflict(err) {
			return nil, err
		}
		if driver.IsConflict(err) {
			// Another goroutine created it concurrently — open it.
			return db.Collection(ctx, name)
		}
		return col, nil
	}
	return nil, err
}

// ──────────────────────────────────────────────────────────────────────────────
// Backend — InitRepo / OpenStorer / DeleteRepo / PurgeRepo
// ──────────────────────────────────────────────────────────────────────────────

// encKey replaces "/" with ":" so the result is a legal ArangoDB document
// key. ArangoDB uses "/" as the collection/key separator in document handles,
// so "/" is not allowed inside a key string.
func encKey(s string) string {
	return strings.ReplaceAll(s, "/", ":")
}

// agencyKey builds a compound ArangoDB document key: "{agencyID}:{suffix}".
// All "/" characters are encoded as ":" because ArangoDB document keys must
// not contain "/".
func agencyKey(agencyID, suffix string) string {
	return encKey(agencyID) + ":" + encKey(suffix)
}

// existsDoc returns true if a document with the given key exists in col.
func existsDoc(ctx context.Context, col driver.Collection, key string) (bool, error) {
	ok, err := col.DocumentExists(ctx, key)
	return ok, err
}

// InitRepo provisions a new Git repository for agencyID in ArangoDB.
// It writes an initial config document and performs a git.Init so that HEAD
// and the initial empty commit exist.
// Returns [codevaldgit.ErrRepoAlreadyExists] if a repository already exists.
func (b *arangoBackend) InitRepo(ctx context.Context, agencyID string) error {
	col, err := b.db.Collection(ctx, collConfig)
	if err != nil {
		return fmt.Errorf("InitRepo %s: open config collection: %w", agencyID, err)
	}

	// Use the config document as the existence sentinel.
	exists, err := existsDoc(ctx, col, agencyKey(agencyID, "config"))
	if err != nil {
		return fmt.Errorf("InitRepo %s: check existence: %w", agencyID, err)
	}
	if exists {
		return codevaldgit.ErrRepoAlreadyExists
	}

	// Build a Storage and initialise a real git repo through it.
	s, err := b.newStorage(ctx, agencyID)
	if err != nil {
		return fmt.Errorf("InitRepo %s: build storage: %w", agencyID, err)
	}
	wt := b.worktree()

	r, err := gogit.Init(s, wt)
	if err != nil {
		return fmt.Errorf("InitRepo %s: git.Init: %w", agencyID, err)
	}

	// go-git's Init only calls SetConfig for filesystem-backed storers.
	// For ArangoDB we must write the config sentinel explicitly so that
	// OpenStorer (and PurgeRepo / DeleteRepo existence checks) work.
	if err := s.SetConfig(config.NewConfig()); err != nil {
		return fmt.Errorf("InitRepo %s: write config sentinel: %w", agencyID, err)
	}

	// Point HEAD at refs/heads/main (go-git defaults to master).
	mainRef := plumbing.NewBranchReferenceName("main")
	if err := r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)); err != nil {
		return fmt.Errorf("InitRepo %s: set HEAD→main: %w", agencyID, err)
	}

	worktreeObj, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("InitRepo %s: get worktree: %w", agencyID, err)
	}
	_, err = worktreeObj.Commit("init", &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  "system",
			Email: "system@codevaldgit",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s: initial commit: %w", agencyID, err)
	}
	return nil
}

// OpenStorer returns an ArangoDB [gogitstorage.Storer] and [billy.Filesystem]
// for agencyID. Returns [codevaldgit.ErrRepoNotFound] if no repo exists.
func (b *arangoBackend) OpenStorer(ctx context.Context, agencyID string) (gogitstorage.Storer, billy.Filesystem, error) {
	col, err := b.db.Collection(ctx, collConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("OpenStorer %s: open config collection: %w", agencyID, err)
	}
	exists, err := existsDoc(ctx, col, agencyKey(agencyID, "config"))
	if err != nil {
		return nil, nil, fmt.Errorf("OpenStorer %s: check existence: %w", agencyID, err)
	}
	if !exists {
		return nil, nil, codevaldgit.ErrRepoNotFound
	}

	s, err := b.newStorage(ctx, agencyID)
	if err != nil {
		return nil, nil, err
	}
	return s, b.worktree(), nil
}

// DeleteRepo marks all documents for agencyID as deleted (auditable soft-delete).
// Returns [codevaldgit.ErrRepoNotFound] if the repo does not exist.
func (b *arangoBackend) DeleteRepo(ctx context.Context, agencyID string) error {
	col, err := b.db.Collection(ctx, collConfig)
	if err != nil {
		return fmt.Errorf("DeleteRepo %s: %w", agencyID, err)
	}
	exists, err := existsDoc(ctx, col, agencyKey(agencyID, "config"))
	if err != nil {
		return fmt.Errorf("DeleteRepo %s: check existence: %w", agencyID, err)
	}
	if !exists {
		return codevaldgit.ErrRepoNotFound
	}
	// Soft-delete: mark the config sentinel as deleted.
	update := map[string]interface{}{"deleted": true}
	if _, err := col.UpdateDocument(ctx, agencyKey(agencyID, "config"), update); err != nil {
		return fmt.Errorf("DeleteRepo %s: mark deleted: %w", agencyID, err)
	}
	return nil
}

// PurgeRepo permanently removes all ArangoDB documents for agencyID.
// Returns [codevaldgit.ErrRepoNotFound] if the repo does not exist.
func (b *arangoBackend) PurgeRepo(ctx context.Context, agencyID string) error {
	cfgCol, err := b.db.Collection(ctx, collConfig)
	if err != nil {
		return fmt.Errorf("PurgeRepo %s: %w", agencyID, err)
	}
	exists, err := existsDoc(ctx, cfgCol, agencyKey(agencyID, "config"))
	if err != nil {
		return fmt.Errorf("PurgeRepo %s: check existence: %w", agencyID, err)
	}
	if !exists {
		return codevaldgit.ErrRepoNotFound
	}

	prefix := encKey(agencyID) + ":"
	for _, collName := range []string{collObjects, collRefs, collIndex, collConfig} {
		if err := b.purgeCollection(ctx, collName, prefix); err != nil {
			return fmt.Errorf("PurgeRepo %s: purge %s: %w", agencyID, collName, err)
		}
	}
	return nil
}

// purgeCollection deletes all documents whose _key starts with prefix.
func (b *arangoBackend) purgeCollection(ctx context.Context, collName, prefix string) error {
	aql := fmt.Sprintf(
		`FOR doc IN %s FILTER STARTS_WITH(doc._key, @prefix) REMOVE doc IN %s`,
		collName, collName,
	)
	cur, err := b.db.Query(ctx, aql, map[string]interface{}{"prefix": prefix})
	if err != nil {
		return err
	}
	return cur.Close()
}

// worktree returns a billy.Filesystem for the working tree.
func (b *arangoBackend) worktree() billy.Filesystem {
	if b.cfg.WorktreePath != "" {
		// TODO: return osfs.New(b.cfg.WorktreePath) for persistent worktree.
		// For MVP the caller almost always wants memfs.
	}
	return memfs.New()
}

// newStorage constructs the ArangoDB-backed Storage for a specific agency.
func (b *arangoBackend) newStorage(ctx context.Context, agencyID string) (*Storage, error) {
	objs, err := b.db.Collection(ctx, collObjects)
	if err != nil {
		return nil, fmt.Errorf("newStorage: open %s: %w", collObjects, err)
	}
	refs, err := b.db.Collection(ctx, collRefs)
	if err != nil {
		return nil, fmt.Errorf("newStorage: open %s: %w", collRefs, err)
	}
	idx, err := b.db.Collection(ctx, collIndex)
	if err != nil {
		return nil, fmt.Errorf("newStorage: open %s: %w", collIndex, err)
	}
	cfg, err := b.db.Collection(ctx, collConfig)
	if err != nil {
		return nil, fmt.Errorf("newStorage: open %s: %w", collConfig, err)
	}
	return &Storage{
		agencyID: agencyID,
		objects:  objs,
		refs:     refs,
		idx:      idx,
		cfg:      cfg,
		modules:  make(map[string]*Storage),
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Storage — implements storage.Storer
// ──────────────────────────────────────────────────────────────────────────────

// Storage implements [gogitstorage.Storer] backed by ArangoDB.
// It satisfies EncodedObjectStorer, ReferenceStorer, ShallowStorer,
// IndexStorer, config.ConfigStorer and ModuleStorer.
type Storage struct {
	agencyID string
	objects  driver.Collection
	refs     driver.Collection
	idx      driver.Collection
	cfg      driver.Collection
	modules  map[string]*Storage
}

// ─── EncodedObjectStorer ──────────────────────────────────────────────────────

// objectDoc is the ArangoDB document schema for git_objects.
type objectDoc struct {
	Key      string `json:"_key"`
	AgencyID string `json:"agencyID"`
	SHA      string `json:"sha"`
	ObjType  string `json:"type"`
	Encoded  string `json:"encoded"` // base64(raw git object bytes)
}

// NewEncodedObject returns a new in-memory plumbing.EncodedObject.
func (s *Storage) NewEncodedObject() plumbing.EncodedObject {
	return &plumbing.MemoryObject{}
}

// SetEncodedObject stores a Git object; returns its hash.
// Writing the same SHA twice is idempotent — no error is returned.
func (s *Storage) SetEncodedObject(obj plumbing.EncodedObject) (plumbing.Hash, error) {
	h := obj.Hash()
	key := agencyKey(s.agencyID, h.String())

	r, err := obj.Reader()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: reader: %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: read: %w", err)
	}

	doc := objectDoc{
		Key:      key,
		AgencyID: s.agencyID,
		SHA:      h.String(),
		ObjType:  obj.Type().String(),
		Encoded:  base64.StdEncoding.EncodeToString(raw),
	}
	_, err = s.objects.CreateDocument(context.Background(), doc)
	if err != nil && !driver.IsConflict(err) {
		return plumbing.ZeroHash, fmt.Errorf("SetEncodedObject: store %s: %w", h, err)
	}
	return h, nil
}

// HasEncodedObject returns nil if the object exists, plumbing.ErrObjectNotFound otherwise.
func (s *Storage) HasEncodedObject(h plumbing.Hash) error {
	ok, err := s.objects.DocumentExists(context.Background(), agencyKey(s.agencyID, h.String()))
	if err != nil {
		return err
	}
	if !ok {
		return plumbing.ErrObjectNotFound
	}
	return nil
}

// EncodedObjectSize returns the size of the raw (decoded) object bytes.
func (s *Storage) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	obj, err := s.EncodedObject(plumbing.AnyObject, h)
	if err != nil {
		return 0, err
	}
	return obj.Size(), nil
}

// EncodedObject retrieves a Git object by type and hash.
// Pass [plumbing.AnyObject] to retrieve regardless of type.
func (s *Storage) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	var doc objectDoc
	_, err := s.objects.ReadDocument(context.Background(), agencyKey(s.agencyID, h.String()), &doc)
	if driver.IsNotFound(err) {
		return nil, plumbing.ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("EncodedObject %s: %w", h, err)
	}

	objType, err := plumbing.ParseObjectType(doc.ObjType)
	if err != nil {
		return nil, fmt.Errorf("EncodedObject %s: unknown type %q", h, doc.ObjType)
	}
	if t != plumbing.AnyObject && objType != t {
		return nil, plumbing.ErrObjectNotFound
	}

	raw, err := base64.StdEncoding.DecodeString(doc.Encoded)
	if err != nil {
		return nil, fmt.Errorf("EncodedObject %s: base64 decode: %w", h, err)
	}

	mo := &plumbing.MemoryObject{}
	mo.SetType(objType)
	mo.SetSize(int64(len(raw)))
	if _, err := mo.Write(raw); err != nil {
		return nil, fmt.Errorf("EncodedObject %s: write to MemoryObject: %w", h, err)
	}
	return mo, nil
}

// IterEncodedObjects returns an iterator over all objects of the given type.
// Pass [plumbing.AnyObject] to iterate all objects for this agency.
func (s *Storage) IterEncodedObjects(t plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	ctx := context.Background()
	var aql string
	var bindVars map[string]interface{}

	if t == plumbing.AnyObject {
		aql = `FOR doc IN git_objects FILTER doc.agencyID == @agency RETURN doc`
		bindVars = map[string]interface{}{"agency": s.agencyID}
	} else {
		aql = `FOR doc IN git_objects FILTER doc.agencyID == @agency AND doc.type == @type RETURN doc`
		bindVars = map[string]interface{}{"agency": s.agencyID, "type": t.String()}
	}

	cur, err := s.objects.Database().Query(ctx, aql, bindVars)
	if err != nil {
		return nil, fmt.Errorf("IterEncodedObjects: query: %w", err)
	}

	var objs []plumbing.EncodedObject
	for cur.HasMore() {
		var doc objectDoc
		if _, err := cur.ReadDocument(ctx, &doc); err != nil {
			cur.Close()
			return nil, fmt.Errorf("IterEncodedObjects: read: %w", err)
		}
		objType, err := plumbing.ParseObjectType(doc.ObjType)
		if err != nil {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(doc.Encoded)
		if err != nil {
			continue
		}
		mo := &plumbing.MemoryObject{}
		mo.SetType(objType)
		mo.SetSize(int64(len(raw)))
		if _, err := mo.Write(raw); err != nil {
			continue
		}
		objs = append(objs, mo)
	}
	cur.Close()
	return storer.NewEncodedObjectSliceIter(objs), nil
}

// AddAlternate is a no-op for the ArangoDB backend.
func (s *Storage) AddAlternate(_ string) error { return nil }

// ─── ReferenceStorer ──────────────────────────────────────────────────────────

// refDoc is the ArangoDB document schema for git_refs.
type refDoc struct {
	Key      string `json:"_key"`
	AgencyID string `json:"agencyID"`
	Name     string `json:"name"`
	Target   string `json:"target"`
	Symbolic bool   `json:"symbolic"`
}

// SetReference stores a reference. Overwrites any existing reference with the same name.
func (s *Storage) SetReference(ref *plumbing.Reference) error {
	if ref == nil {
		return nil
	}
	key := agencyKey(s.agencyID, ref.Name().String())
	doc := refDoc{
		Key:      key,
		AgencyID: s.agencyID,
		Name:     ref.Name().String(),
		Symbolic: ref.Type() == plumbing.SymbolicReference,
	}
	if ref.Type() == plumbing.SymbolicReference {
		doc.Target = ref.Target().String()
	} else {
		doc.Target = ref.Hash().String()
	}
	ctx := context.Background()
	exists, err := s.refs.DocumentExists(ctx, key)
	if err != nil {
		return fmt.Errorf("SetReference %s: check exists: %w", ref.Name(), err)
	}
	if exists {
		_, err = s.refs.UpdateDocument(ctx, key, doc)
	} else {
		_, err = s.refs.CreateDocument(ctx, doc)
	}
	if err != nil {
		return fmt.Errorf("SetReference %s: %w", ref.Name(), err)
	}
	return nil
}

// CheckAndSetReference sets new only if old is nil or matches the stored value.
func (s *Storage) CheckAndSetReference(new, old *plumbing.Reference) error {
	if new == nil {
		return nil
	}
	if old != nil {
		existing, err := s.Reference(old.Name())
		if err != nil {
			return err
		}
		if existing.Hash() != old.Hash() {
			return gogitstorage.ErrReferenceHasChanged
		}
	}
	return s.SetReference(new)
}

// Reference returns the reference with the given name.
// Returns [plumbing.ErrReferenceNotFound] if not found.
func (s *Storage) Reference(n plumbing.ReferenceName) (*plumbing.Reference, error) {
	var doc refDoc
	_, err := s.refs.ReadDocument(context.Background(), agencyKey(s.agencyID, n.String()), &doc)
	if driver.IsNotFound(err) {
		return nil, plumbing.ErrReferenceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Reference %s: %w", n, err)
	}
	return docToRef(doc), nil
}

// IterReferences returns an iterator over all references for this agency.
func (s *Storage) IterReferences() (storer.ReferenceIter, error) {
	ctx := context.Background()
	aql := `FOR doc IN git_refs FILTER doc.agencyID == @agency RETURN doc`
	cur, err := s.refs.Database().Query(ctx, aql, map[string]interface{}{"agency": s.agencyID})
	if err != nil {
		return nil, fmt.Errorf("IterReferences: query: %w", err)
	}
	var refs []*plumbing.Reference
	for cur.HasMore() {
		var doc refDoc
		if _, err := cur.ReadDocument(ctx, &doc); err != nil {
			cur.Close()
			return nil, err
		}
		refs = append(refs, docToRef(doc))
	}
	cur.Close()
	return storer.NewReferenceSliceIter(refs), nil
}

// RemoveReference removes the reference with the given name (no-op if absent).
func (s *Storage) RemoveReference(n plumbing.ReferenceName) error {
	_, err := s.refs.RemoveDocument(context.Background(), agencyKey(s.agencyID, n.String()))
	if driver.IsNotFound(err) {
		return nil
	}
	return err
}

// CountLooseRefs returns the number of references stored for this agency.
func (s *Storage) CountLooseRefs() (int, error) {
	ctx := context.Background()
	aql := `RETURN LENGTH(FOR doc IN git_refs FILTER doc.agencyID == @agency RETURN 1)`
	cur, err := s.refs.Database().Query(ctx, aql, map[string]interface{}{"agency": s.agencyID})
	if err != nil {
		return 0, fmt.Errorf("CountLooseRefs: %w", err)
	}
	defer cur.Close()
	var count int
	if cur.HasMore() {
		if _, err := cur.ReadDocument(ctx, &count); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// PackRefs is a no-op for ArangoDB (no pack files).
func (s *Storage) PackRefs() error { return nil }

// docToRef converts a refDoc to a plumbing.Reference.
func docToRef(doc refDoc) *plumbing.Reference {
	name := plumbing.ReferenceName(doc.Name)
	if doc.Symbolic {
		return plumbing.NewSymbolicReference(name, plumbing.ReferenceName(doc.Target))
	}
	return plumbing.NewHashReference(name, plumbing.NewHash(doc.Target))
}

// ─── ShallowStorer ────────────────────────────────────────────────────────────

// shallowDoc is the ArangoDB schema for shallow commit lists.
type shallowDoc struct {
	Key      string   `json:"_key"`
	AgencyID string   `json:"agencyID"`
	Commits  []string `json:"commits"`
}

// SetShallow stores the list of shallow commit SHAs.
func (s *Storage) SetShallow(commits []plumbing.Hash) error {
	key := agencyKey(s.agencyID, "shallow")
	strs := make([]string, len(commits))
	for i, h := range commits {
		strs[i] = h.String()
	}
	doc := shallowDoc{Key: key, AgencyID: s.agencyID, Commits: strs}
	ctx := context.Background()
	ok, err := s.cfg.DocumentExists(ctx, key)
	if err != nil {
		return err
	}
	if ok {
		_, err = s.cfg.UpdateDocument(ctx, key, doc)
	} else {
		_, err = s.cfg.CreateDocument(ctx, doc)
	}
	return err
}

// Shallow returns the list of shallow commit SHAs.
func (s *Storage) Shallow() ([]plumbing.Hash, error) {
	key := agencyKey(s.agencyID, "shallow")
	var doc shallowDoc
	_, err := s.cfg.ReadDocument(context.Background(), key, &doc)
	if driver.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hashes := make([]plumbing.Hash, len(doc.Commits))
	for i, s := range doc.Commits {
		hashes[i] = plumbing.NewHash(s)
	}
	return hashes, nil
}

// ─── IndexStorer ──────────────────────────────────────────────────────────────

// indexDoc is the ArangoDB schema for the git staging index.
type indexDoc struct {
	Key      string `json:"_key"`
	AgencyID string `json:"agencyID"`
	Encoded  string `json:"encoded"` // base64(encoded index file bytes)
}

// SetIndex serialises and stores the git index.
func (s *Storage) SetIndex(idx *gitindex.Index) error {
	var buf bytes.Buffer
	if err := gitindex.NewEncoder(&buf).Encode(idx); err != nil {
		return fmt.Errorf("SetIndex: encode: %w", err)
	}
	key := agencyKey(s.agencyID, "index")
	doc := indexDoc{
		Key:      key,
		AgencyID: s.agencyID,
		Encoded:  base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
	ctx := context.Background()
	ok, err := s.idx.DocumentExists(ctx, key)
	if err != nil {
		return err
	}
	if ok {
		_, err = s.idx.UpdateDocument(ctx, key, doc)
	} else {
		_, err = s.idx.CreateDocument(ctx, doc)
	}
	return err
}

// Index reads and deserialises the git index.
// Returns an empty index if none has been stored yet.
func (s *Storage) Index() (*gitindex.Index, error) {
	key := agencyKey(s.agencyID, "index")
	var doc indexDoc
	_, err := s.idx.ReadDocument(context.Background(), key, &doc)
	if driver.IsNotFound(err) {
		return &gitindex.Index{Version: 2}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Index: read: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Encoded)
	if err != nil {
		return nil, fmt.Errorf("Index: base64 decode: %w", err)
	}
	idx := &gitindex.Index{}
	if err := gitindex.NewDecoder(bytes.NewReader(raw)).Decode(idx); err != nil {
		return nil, fmt.Errorf("Index: decode: %w", err)
	}
	return idx, nil
}

// ─── ConfigStorer ─────────────────────────────────────────────────────────────

// configDocSchema is the ArangoDB schema for the git repo config.
type configDocSchema struct {
	Key      string `json:"_key"`
	AgencyID string `json:"agencyID"`
	Encoded  string `json:"encoded"` // base64(marshal'd git config bytes)
	Deleted  bool   `json:"deleted,omitempty"`
}

// SetConfig serialises and stores the repo config.
func (s *Storage) SetConfig(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	raw, err := cfg.Marshal()
	if err != nil {
		return fmt.Errorf("SetConfig: marshal: %w", err)
	}
	key := agencyKey(s.agencyID, "config")
	doc := configDocSchema{
		Key:      key,
		AgencyID: s.agencyID,
		Encoded:  base64.StdEncoding.EncodeToString(raw),
	}
	ctx := context.Background()
	ok, err := s.cfg.DocumentExists(ctx, key)
	if err != nil {
		return err
	}
	if ok {
		_, err = s.cfg.UpdateDocument(ctx, key, doc)
	} else {
		_, err = s.cfg.CreateDocument(ctx, doc)
	}
	return err
}

// Config reads and deserialises the repo config.
// Returns a default config if none has been stored yet.
func (s *Storage) Config() (*config.Config, error) {
	key := agencyKey(s.agencyID, "config")
	var doc configDocSchema
	_, err := s.cfg.ReadDocument(context.Background(), key, &doc)
	if driver.IsNotFound(err) {
		return config.NewConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("Config: read: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Encoded)
	if err != nil {
		return nil, fmt.Errorf("Config: base64 decode: %w", err)
	}
	cfg := config.NewConfig()
	if err := cfg.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("Config: unmarshal: %w", err)
	}
	return cfg, nil
}

// ─── ModuleStorer ─────────────────────────────────────────────────────────────

// Module returns (or creates) a sub-Storage for a Git submodule.
func (s *Storage) Module(name string) (gogitstorage.Storer, error) {
	if m, ok := s.modules[name]; ok {
		return m, nil
	}
	// Submodules use a namespaced agencyID: "{agencyID}/module/{name}"
	sub := &Storage{
		agencyID: s.agencyID + "/module/" + strings.ReplaceAll(name, "/", "_"),
		objects:  s.objects,
		refs:     s.refs,
		idx:      s.idx,
		cfg:      s.cfg,
		modules:  make(map[string]*Storage),
	}
	s.modules[name] = sub
	return sub, nil
}
