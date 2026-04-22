package gitgraph

import (
	"encoding/json"
	"fmt"
)

// SignalVocab holds the parsed contents of .git-graph/.signals.json.
// If the file is absent, DefaultSignals is used.
type SignalVocab struct {
	Signals []SignalDef `json:"signals"`
}

// SignalDef is a single entry in the signal vocabulary.
type SignalDef struct {
	Name        string `json:"name"`
	Layer       int    `json:"layer"`
	Description string `json:"description"`
}

// DefaultSignals is the built-in signal vocabulary used when .signals.json
// is absent or malformed.
var DefaultSignals = SignalVocab{
	Signals: []SignalDef{
		{Name: "surface", Layer: 2, Description: "Keyword appears but file does not own the concept"},
		{Name: "index", Layer: 5, Description: "File lists or links to other files on this topic"},
		{Name: "structural", Layer: 8, Description: "File defines schema, format, status model, or process"},
		{Name: "contributor", Layer: 12, Description: "File adds content other files depend on"},
		{Name: "authority", Layer: 18, Description: "Canonical source — other files reference this one"},
	},
}

// ValidDescriptors is the set of allowed descriptor strings for file→file reference edges.
var ValidDescriptors = map[string]bool{
	"depends_on":  true,
	"test_for":    true,
	"tested_by":   true,
	"documents":   true,
	"obsoletes":   true,
	"contradicts": true,
	"references":  true,
}

// MappingFile is the parsed representation of a single .git-graph/*.json file.
type MappingFile struct {
	Keywords []KeywordDef   `json:"keywords"`
	Mappings []MappingEntry `json:"mappings"`
}

// KeywordDef declares a keyword to upsert into the agency taxonomy.
type KeywordDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Parent      string `json:"parent"`
}

// MappingEntry declares edges for a single file.
type MappingEntry struct {
	File       string          `json:"file"`
	Keywords   []string        `json:"keywords"`
	Depths     []DepthEntry    `json:"depths"`
	TestedBy   []TestedByEntry `json:"tested_by"`
	References []RefEntry      `json:"references"`
}

// DepthEntry carries the signal depth and optional note for a single
// keyword attachment on a MappingEntry. It enriches the tagged_with edge
// with signal and note properties.
type DepthEntry struct {
	Keyword string `json:"keyword"` // must appear in MappingEntry.Keywords
	Signal  string `json:"signal"`  // must be in the active SignalVocab
	Note    string `json:"note"`
}

// TestedByEntry declares a references {descriptor:"tested_by"} edge from
// this mapping's file to the target file.
type TestedByEntry struct {
	File string `json:"file"`
}

// RefEntry is a single file→file reference edge declaration.
type RefEntry struct {
	File       string `json:"file"`
	Descriptor string `json:"descriptor"`
}

// ParseSignalVocab parses the contents of .git-graph/.signals.json.
// Returns DefaultSignals and a non-nil error if data is empty, malformed,
// or contains no signal entries.
func ParseSignalVocab(data []byte) (SignalVocab, error) {
	var vocab SignalVocab
	if err := json.Unmarshal(data, &vocab); err != nil {
		return DefaultSignals, ErrInvalidMappingFile{
			File:    ".git-graph/.signals.json",
			Details: []string{fmt.Sprintf("JSON parse error: %s", err.Error())},
		}
	}
	if len(vocab.Signals) == 0 {
		return DefaultSignals, ErrInvalidMappingFile{
			File:    ".git-graph/.signals.json",
			Details: []string{"signals array is empty"},
		}
	}
	return vocab, nil
}

// ParseMappingFile parses and validates a single .git-graph JSON file.
// vocab is the active signal vocabulary used to validate signal values in
// depths[] entries. Returns ErrInvalidMappingFile with all validation
// problems if any are found.
func ParseMappingFile(data []byte, vocab SignalVocab) (MappingFile, error) {
	var mf MappingFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return MappingFile{}, ErrInvalidMappingFile{
			Details: []string{fmt.Sprintf("JSON parse error: %s", err.Error())},
		}
	}

	validSignals := buildSignalSet(vocab)
	var problems []string

	problems = append(problems, validateKeywords(mf.Keywords)...)
	problems = append(problems, validateMappings(mf.Mappings, validSignals)...)

	if len(problems) > 0 {
		return MappingFile{}, ErrInvalidMappingFile{Details: problems}
	}
	return mf, nil
}

// buildSignalSet returns a set of valid signal names from the vocabulary.
func buildSignalSet(vocab SignalVocab) map[string]bool {
	set := make(map[string]bool, len(vocab.Signals))
	for _, s := range vocab.Signals {
		set[s.Name] = true
	}
	return set
}

// validateKeywords checks all keyword definitions and returns any problems.
func validateKeywords(defs []KeywordDef) []string {
	var problems []string
	seen := make(map[string]bool, len(defs))
	for i, kw := range defs {
		if kw.Name == "" {
			problems = append(problems, fmt.Sprintf("keywords[%d]: name is empty", i))
			continue
		}
		if seen[kw.Name] {
			problems = append(problems, fmt.Sprintf("keywords[%d]: duplicate name %q", i, kw.Name))
		}
		seen[kw.Name] = true
	}
	return problems
}

// validateMappings checks all mapping entries and returns any problems.
func validateMappings(entries []MappingEntry, validSignals map[string]bool) []string {
	var problems []string
	for i, entry := range entries {
		prefix := fmt.Sprintf("mappings[%d]", i)
		if entry.File == "" {
			problems = append(problems, fmt.Sprintf("%s: file is empty", prefix))
		}
		problems = append(problems, validateDepths(prefix, entry.Keywords, entry.Depths, validSignals)...)
		problems = append(problems, validateTestedBy(prefix, entry.TestedBy)...)
		problems = append(problems, validateRefs(prefix, entry.References)...)
	}
	return problems
}

// validateDepths checks depths[] entries for a single mapping.
func validateDepths(prefix string, keywords []string, depths []DepthEntry, validSignals map[string]bool) []string {
	kwSet := make(map[string]bool, len(keywords))
	for _, kw := range keywords {
		kwSet[kw] = true
	}

	var problems []string
	for j, d := range depths {
		loc := fmt.Sprintf("%s.depths[%d]", prefix, j)
		if !kwSet[d.Keyword] {
			problems = append(problems, fmt.Sprintf("%s: keyword %q not in same mapping's keywords list", loc, d.Keyword))
		}
		if !validSignals[d.Signal] {
			problems = append(problems, fmt.Sprintf("%s: signal %q is not in the active signal vocabulary", loc, d.Signal))
		}
	}
	return problems
}

// validateTestedBy checks tested_by[] entries for a single mapping.
func validateTestedBy(prefix string, entries []TestedByEntry) []string {
	var problems []string
	for j, tb := range entries {
		if tb.File == "" {
			problems = append(problems, fmt.Sprintf("%s.tested_by[%d]: file is empty", prefix, j))
		}
	}
	return problems
}

// validateRefs checks references[] entries for a single mapping.
func validateRefs(prefix string, refs []RefEntry) []string {
	var problems []string
	for j, ref := range refs {
		loc := fmt.Sprintf("%s.references[%d]", prefix, j)
		if ref.File == "" {
			problems = append(problems, fmt.Sprintf("%s: file is empty", loc))
		}
		if !ValidDescriptors[ref.Descriptor] {
			problems = append(problems, fmt.Sprintf("%s: descriptor %q is not valid", loc, ref.Descriptor))
		}
	}
	return problems
}
