// Package server provides the inbound gRPC GitService handler and the Git Smart
// HTTP handler for CodeValdGit.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gogitserver "github.com/go-git/go-git/v5/plumbing/transport/server"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// GitHTTPHandler serves the Git Smart HTTP protocol for all agencies.
//
// URL structure:
//
//	GET  /{agencyID}/{repoName}/info/refs?service=git-upload-pack   — ref advertisement (fetch/clone)
//	GET  /{agencyID}/{repoName}/info/refs?service=git-receive-pack  — ref advertisement (push)
//	POST /{agencyID}/{repoName}/git-upload-pack                     — pack transfer (fetch/clone)
//	POST /{agencyID}/{repoName}/git-receive-pack                    — pack transfer (push)
//
// The handler is designed to be served via cmux alongside the gRPC server on a
// single port (see GIT-009 / cmd/main.go).
type GitHTTPHandler struct {
	srv transport.Transport
}

// NewGitHTTPHandler constructs a GitHTTPHandler backed by the given Backend.
// b must be a filesystem backend (or any Backend whose OpenStorer returns a
// go-git storage.Storer backed by a real .git object store).
func NewGitHTTPHandler(b codevaldgit.Backend) *GitHTTPHandler {
	loader := &backendLoader{b: b}
	return &GitHTTPHandler{srv: gogitserver.NewServer(loader)}
}

// ServeHTTP implements http.Handler.
// It routes the four Smart HTTP endpoints to their respective handlers.
func (h *GitHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agencyID, repoName, rest, ok := extractAgencyRepo(r.URL.Path)
	if !ok || agencyID == "" || repoName == "" {
		http.Error(w, "invalid repository path", http.StatusBadRequest)
		return
	}

	switch {
	case r.Method == http.MethodGet && rest == "/info/refs":
		h.infoRefs(w, r, agencyID, repoName)
	case r.Method == http.MethodPost && rest == "/git-upload-pack":
		h.uploadPack(w, r, agencyID, repoName)
	case r.Method == http.MethodPost && rest == "/git-receive-pack":
		h.receivePack(w, r, agencyID, repoName)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// infoRefs handles GET /{agencyID}/{repoName}/info/refs?service=git-{upload,receive}-pack.
// It emits the Smart HTTP service announcement followed by the advertised refs.
func (h *GitHTTPHandler) infoRefs(w http.ResponseWriter, r *http.Request, agencyID, repoName string) {
	service := r.URL.Query().Get("service")
	switch service {
	case transport.UploadPackServiceName, transport.ReceivePackServiceName:
	default:
		http.Error(w, "unsupported service", http.StatusForbidden)
		return
	}

	ep, err := endpointFor(agencyID, repoName)
	if err != nil {
		http.Error(w, "bad endpoint", http.StatusInternalServerError)
		return
	}

	var advRefs *packp.AdvRefs

	if service == transport.UploadPackServiceName {
		sess, err := h.srv.NewUploadPackSession(ep, nil)
		if err != nil {
			httpErrorFromTransport(w, err)
			return
		}
		defer sess.Close() //nolint:errcheck

		advRefs, err = sess.AdvertisedReferencesContext(r.Context())
		if err != nil {
			httpErrorFromTransport(w, err)
			return
		}
	} else {
		sess, err := h.srv.NewReceivePackSession(ep, nil)
		if err != nil {
			httpErrorFromTransport(w, err)
			return
		}
		defer sess.Close() //nolint:errcheck

		advRefs, err = sess.AdvertisedReferencesContext(r.Context())
		if err != nil {
			httpErrorFromTransport(w, err)
			return
		}
	}

	// Prepend the Smart HTTP service header ("# service=git-…\n" + flush-pkt).
	// packp.AdvRefs.Encode will emit these prefix entries before the ref list.
	advRefs.Prefix = [][]byte{
		[]byte(fmt.Sprintf("# service=%s", service)),
		pktline.Flush,
	}

	contentType := fmt.Sprintf("application/x-git-%s-advertisement",
		strings.TrimPrefix(service, "git-"))

	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)

	if err := advRefs.Encode(w); err != nil {
		// Headers already sent; nothing useful we can do.
		return
	}
}

// uploadPack handles POST /{agencyID}/{repoName}/git-upload-pack (fetch / clone).
func (h *GitHTTPHandler) uploadPack(w http.ResponseWriter, r *http.Request, agencyID, repoName string) {
	ep, err := endpointFor(agencyID, repoName)
	if err != nil {
		http.Error(w, "bad endpoint", http.StatusInternalServerError)
		return
	}

	sess, err := h.srv.NewUploadPackSession(ep, nil)
	if err != nil {
		httpErrorFromTransport(w, err)
		return
	}
	defer sess.Close() //nolint:errcheck

	// AdvertisedReferencesContext must be called before UploadPack to
	// initialise the session state inside go-git.
	if _, err := sess.AdvertisedReferencesContext(r.Context()); err != nil {
		httpErrorFromTransport(w, err)
		return
	}

	req := packp.NewUploadPackRequest()
	if err := req.Decode(r.Body); err != nil {
		http.Error(w, "malformed upload-pack request: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := sess.UploadPack(r.Context(), req)
	if err != nil {
		httpErrorFromTransport(w, err)
		return
	}

	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.WriteHeader(http.StatusOK)

	_ = resp.Encode(w)
}

// receivePack handles POST /{agencyID}/{repoName}/git-receive-pack (push).
func (h *GitHTTPHandler) receivePack(w http.ResponseWriter, r *http.Request, agencyID, repoName string) {
	ep, err := endpointFor(agencyID, repoName)
	if err != nil {
		http.Error(w, "bad endpoint", http.StatusInternalServerError)
		return
	}

	sess, err := h.srv.NewReceivePackSession(ep, nil)
	if err != nil {
		httpErrorFromTransport(w, err)
		return
	}
	defer sess.Close() //nolint:errcheck

	// AdvertisedReferencesContext must be called before ReceivePack.
	if _, err := sess.AdvertisedReferencesContext(r.Context()); err != nil {
		httpErrorFromTransport(w, err)
		return
	}

	req := packp.NewReferenceUpdateRequest()
	if err := req.Decode(r.Body); err != nil {
		http.Error(w, "malformed receive-pack request: "+err.Error(), http.StatusBadRequest)
		return
	}

	status, err := sess.ReceivePack(r.Context(), req)
	if err != nil {
		httpErrorFromTransport(w, err)
		return
	}

	setNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.WriteHeader(http.StatusOK)

	_ = status.Encode(w)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// backendLoader implements gogitserver.Loader by delegating to a codevaldgit.Backend.
// ep.Path is expected to be "/{agencyID}" or "/{agencyID}/"; the agencyID is
// extracted by trimming leading/trailing slashes.
type backendLoader struct {
	b codevaldgit.Backend
}

// Load satisfies gogitserver.Loader.
// ep.Path is expected to be "/{agencyID}/{repoName}" — both segments are
// extracted and forwarded to Backend.OpenStorer.
// If the repository does not yet exist it is created automatically via
// Backend.InitRepo before retrying OpenStorer.
// Returns transport.ErrRepositoryNotFound only when OpenStorer fails after
// the auto-create attempt.
func (l *backendLoader) Load(ep *transport.Endpoint) (storer.Storer, error) {
	trimmed := strings.Trim(ep.Path, "/")
	if trimmed == "" {
		return nil, transport.ErrRepositoryNotFound
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, transport.ErrRepositoryNotFound
	}
	agencyID, repoName := parts[0], parts[1]

	ctx := context.Background()
	sto, _, err := l.b.OpenStorer(ctx, agencyID, repoName)
	if err == nil {
		return sto, nil
	}

	// Auto-create the repository on first access and retry.
	if errors.Is(err, codevaldgit.ErrRepoNotFound) {
		log.Printf("[INFO] backendLoader: repo %s/%s not found — auto-creating", agencyID, repoName)
		if initErr := l.b.InitRepo(ctx, agencyID, repoName); initErr != nil && !errors.Is(initErr, codevaldgit.ErrRepoAlreadyExists) {
			log.Printf("[ERROR] backendLoader: InitRepo %s/%s: %v", agencyID, repoName, initErr)
			return nil, transport.ErrRepositoryNotFound
		}
		sto, _, err = l.b.OpenStorer(ctx, agencyID, repoName)
		if err != nil {
			log.Printf("[ERROR] backendLoader: OpenStorer after init %s/%s: %v", agencyID, repoName, err)
			return nil, transport.ErrRepositoryNotFound
		}
		return sto, nil
	}

	return nil, transport.ErrRepositoryNotFound
}

// endpointFor builds a transport.Endpoint whose Path is "/{agencyID}/{repoName}".
// go-git's server.Loader uses Endpoint.Path as the repository key.
func endpointFor(agencyID, repoName string) (*transport.Endpoint, error) {
	return transport.NewEndpoint(fmt.Sprintf("/%s/%s", agencyID, repoName))
}

// extractAgencyRepo splits a URL path of the form "/{agencyID}/{repoName}/rest"
// into (agencyID, repoName, "/rest", true).
// Returns ("", "", "", false) on bad input (missing agencyID, repoName, or rest).
func extractAgencyRepo(path string) (agencyID, repoName, rest string, ok bool) {
	// Strip leading slash.
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", "", "", false
	}

	// First segment: agencyID.
	idx := strings.Index(trimmed, "/")
	if idx < 0 {
		return "", "", "", false
	}
	agency := trimmed[:idx]
	after := trimmed[idx+1:] // everything after the first slash

	// Second segment: repoName.
	idx2 := strings.Index(after, "/")
	if idx2 < 0 {
		return "", "", "", false
	}
	repo := after[:idx2]
	restSuffix := after[idx2:] // includes the leading slash

	if agency == "" || repo == "" || restSuffix == "/" || restSuffix == "" {
		return "", "", "", false
	}
	return agency, repo, restSuffix, true
}

// setNoCacheHeaders sets the standard Git Smart HTTP cache-control headers.
func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
}

// httpErrorFromTransport maps transport-layer errors to appropriate HTTP status codes.
func httpErrorFromTransport(w http.ResponseWriter, err error) {
	if err == transport.ErrRepositoryNotFound {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}
	http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
}
