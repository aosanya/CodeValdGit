// Package gitgraph provides the parser and sync logic for .git-graph/ push sync.
package gitgraph

import (
	"fmt"
	"strings"
)

// ErrInvalidMappingFile is returned when a .git-graph JSON file fails
// validation. File is the repo-relative path; Details lists each problem.
type ErrInvalidMappingFile struct {
	File    string
	Details []string
}

// Error implements the error interface.
func (e ErrInvalidMappingFile) Error() string {
	return fmt.Sprintf("invalid mapping file %q: %s", e.File, strings.Join(e.Details, "; "))
}
