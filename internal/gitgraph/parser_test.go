package gitgraph_test

import (
	"errors"
	"testing"

	"github.com/aosanya/CodeValdGit/internal/gitgraph"
)

// ── ParseSignalVocab ──────────────────────────────────────────────────────────

func TestParseSignalVocab_Valid(t *testing.T) {
	data := []byte(`{"signals":[{"name":"surface","layer":2,"description":"desc"}]}`)
	vocab, err := gitgraph.ParseSignalVocab(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vocab.Signals) != 1 || vocab.Signals[0].Name != "surface" {
		t.Fatalf("unexpected vocab: %+v", vocab)
	}
}

func TestParseSignalVocab_MalformedJSON(t *testing.T) {
	_, err := gitgraph.ParseSignalVocab([]byte(`{bad json`))
	var e gitgraph.ErrInvalidMappingFile
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidMappingFile, got %T: %v", err, err)
	}
}

func TestParseSignalVocab_EmptySignals(t *testing.T) {
	_, err := gitgraph.ParseSignalVocab([]byte(`{"signals":[]}`))
	var e gitgraph.ErrInvalidMappingFile
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidMappingFile, got %T: %v", err, err)
	}
}

func TestParseSignalVocab_FallsBackToDefault(t *testing.T) {
	_, err := gitgraph.ParseSignalVocab([]byte(`{"signals":[]}`))
	if err == nil {
		t.Fatal("expected error for empty signals")
	}
	// DefaultSignals is well-formed
	if len(gitgraph.DefaultSignals.Signals) == 0 {
		t.Fatal("DefaultSignals must be non-empty")
	}
}

// ── ParseMappingFile — happy path ─────────────────────────────────────────────

func TestParseMappingFile_EmptyFile(t *testing.T) {
	data := []byte(`{}`)
	mf, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mf.Keywords) != 0 || len(mf.Mappings) != 0 {
		t.Fatalf("expected empty MappingFile, got %+v", mf)
	}
}

func TestParseMappingFile_ValidFull(t *testing.T) {
	data := []byte(`{
		"keywords": [
			{"name": "auth", "description": "Auth domain", "scope": "agency"},
			{"name": "login", "scope": "repo", "parent": "auth"}
		],
		"mappings": [
			{
				"file": "lib/login.dart",
				"keywords": ["auth", "login"],
				"depths": [
					{"keyword": "auth",  "signal": "authority", "note": "canonical"},
					{"keyword": "login", "signal": "contributor"}
				],
				"tested_by": [{"file": "test/login_test.dart"}],
				"references": [
					{"file": "lib/provider.dart", "descriptor": "depends_on"},
					{"file": "docs/auth.md",      "descriptor": "documents"}
				]
			}
		]
	}`)
	mf, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mf.Keywords) != 2 {
		t.Fatalf("expected 2 keywords, got %d", len(mf.Keywords))
	}
	if len(mf.Mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mf.Mappings))
	}
	m := mf.Mappings[0]
	if m.File != "lib/login.dart" {
		t.Fatalf("unexpected file: %s", m.File)
	}
	if len(m.Depths) != 2 {
		t.Fatalf("expected 2 depth entries, got %d", len(m.Depths))
	}
	if len(m.TestedBy) != 1 {
		t.Fatalf("expected 1 tested_by, got %d", len(m.TestedBy))
	}
	if len(m.References) != 2 {
		t.Fatalf("expected 2 references, got %d", len(m.References))
	}
}

// ── ParseMappingFile — validation errors ──────────────────────────────────────

func TestParseMappingFile_MalformedJSON(t *testing.T) {
	_, err := gitgraph.ParseMappingFile([]byte(`{bad`), gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_EmptyKeywordName(t *testing.T) {
	data := []byte(`{"keywords":[{"name":""}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_DuplicateKeywordName(t *testing.T) {
	data := []byte(`{"keywords":[{"name":"auth"},{"name":"auth"}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_EmptyMappingFile(t *testing.T) {
	data := []byte(`{"mappings":[{"file":""}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_InvalidDescriptor(t *testing.T) {
	data := []byte(`{"mappings":[{"file":"a.go","references":[{"file":"b.go","descriptor":"not_valid"}]}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_DepthSignalNotInVocab(t *testing.T) {
	data := []byte(`{"mappings":[{
		"file":"a.go",
		"keywords":["auth"],
		"depths":[{"keyword":"auth","signal":"unknown_signal"}]
	}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_DepthKeywordNotInMappingKeywords(t *testing.T) {
	data := []byte(`{"mappings":[{
		"file":"a.go",
		"keywords":["auth"],
		"depths":[{"keyword":"other","signal":"surface"}]
	}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_EmptyTestedByFile(t *testing.T) {
	data := []byte(`{"mappings":[{"file":"a.go","tested_by":[{"file":""}]}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	assertInvalidMappingFile(t, err)
}

func TestParseMappingFile_CustomVocab(t *testing.T) {
	vocab := gitgraph.SignalVocab{
		Signals: []gitgraph.SignalDef{
			{Name: "custom_signal", Layer: 99},
		},
	}
	// custom_signal is valid; "surface" (from DefaultSignals) is not in this vocab
	data := []byte(`{"mappings":[{
		"file":"a.go",
		"keywords":["kw"],
		"depths":[{"keyword":"kw","signal":"custom_signal"}]
	}]}`)
	_, err := gitgraph.ParseMappingFile(data, vocab)
	if err != nil {
		t.Fatalf("unexpected error with custom vocab: %v", err)
	}
}

func TestParseMappingFile_AllValidDescriptors(t *testing.T) {
	descriptors := []string{"depends_on", "test_for", "tested_by", "documents", "obsoletes", "contradicts", "references"}
	for _, d := range descriptors {
		data := []byte(`{"mappings":[{"file":"a.go","references":[{"file":"b.go","descriptor":"` + d + `"}]}]}`)
		_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
		if err != nil {
			t.Fatalf("descriptor %q should be valid, got error: %v", d, err)
		}
	}
}

func TestParseMappingFile_MultipleErrors(t *testing.T) {
	data := []byte(`{"keywords":[{"name":""},{"name":"auth"},{"name":"auth"}],
		"mappings":[{"file":"","references":[{"file":"b.go","descriptor":"bad"}]}]}`)
	_, err := gitgraph.ParseMappingFile(data, gitgraph.DefaultSignals)
	var e gitgraph.ErrInvalidMappingFile
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidMappingFile, got %T: %v", err, err)
	}
	if len(e.Details) < 3 {
		t.Fatalf("expected at least 3 detail messages, got %d: %v", len(e.Details), e.Details)
	}
}

// ── ErrInvalidMappingFile ─────────────────────────────────────────────────────

func TestErrInvalidMappingFile_ErrorString(t *testing.T) {
	e := gitgraph.ErrInvalidMappingFile{
		File:    "auth.json",
		Details: []string{"name is empty", "duplicate name"},
	}
	msg := e.Error()
	if msg == "" {
		t.Fatal("expected non-empty error string")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertInvalidMappingFile(t *testing.T, err error) {
	t.Helper()
	var e gitgraph.ErrInvalidMappingFile
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidMappingFile, got %T: %v", err, err)
	}
}
