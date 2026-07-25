package cli

import (
	"errors"
	"strconv"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
)

// TestExitCodeTablesCoverExitCodeFor is the ADR 0001 conformance guard: every
// whole-invocation failure code that core.ExitCodeFor can return must be
// declared as data in each command's exit-code table. Prose alone is not
// acceptable (ADR 0001), so this test pins the two halves together and fails if
// ExitCodeFor grows a class the registry does not describe.
func TestExitCodeTablesCoverExitCodeFor(t *testing.T) {
	// One representative error per whole-invocation failure class ExitCodeFor
	// can classify, spanning both the sentinels and the FeedError categories.
	failureErrs := []error{
		core.ErrUsage,
		core.ErrSchemaTooNew,
		core.ErrStoreUnavailable,
		core.ErrConfig,
		&core.FeedError{Category: core.CatUsage},
		&core.FeedError{Category: core.CatConfig},
		&core.FeedError{Category: core.CatStore},
		&core.FeedError{Category: core.CatInternal},
		errors.New("unclassified"),
	}

	// Collect the distinct non-zero codes ExitCodeFor actually produces.
	produced := map[string]bool{}
	for _, err := range failureErrs {
		code := core.ExitCodeFor(err)
		if code == 0 {
			t.Errorf("ExitCodeFor(%v) = 0, want a whole-invocation failure code", err)
			continue
		}
		if code >= 1 && code <= 63 {
			t.Errorf("ExitCodeFor(%v) = %d, but 1-63 is reserved for result classes (ADR 0001)", err, code)
		}
		produced[strconv.Itoa(code)] = true
	}

	// Every command's declared table must be a superset of the produced failure
	// codes. defaultExitCodes underlies every non-poll command; the poll and
	// check tables extend it with result sub-codes.
	tables := map[string]map[string]string{
		"default": defaultExitCodes(),
		"poll":    pollExitCodes(),
		"check":   checkExitCodes(),
	}
	for name, table := range tables {
		for code := range produced {
			if _, ok := table[code]; !ok {
				t.Errorf("%s table missing failure code %q that ExitCodeFor produces: %v", name, code, table)
			}
		}
		if _, ok := table["0"]; !ok {
			t.Errorf("%s table missing success code 0: %v", name, table)
		}
	}

	// The poll and check tables must declare the result sub-codes 2 and 3, which
	// the e2e suite asserts as real exits for the all_failed and partial
	// scenarios.
	for _, name := range []string{"poll", "check"} {
		for _, code := range []string{"2", "3"} {
			if _, ok := tables[name][code]; !ok {
				t.Errorf("%s table missing result sub-code %q: %v", name, code, tables[name])
			}
		}
	}
}
