package commands

import (
	"encoding/json"
	"testing"
)

// The verdict rule under test: a mutation is NOT_APPLIED only when Trakt
// refused something (`not_found` / `skipped_ids`) AND nothing was applied.
//
// The two idempotency cases are the reason the discriminator is `not_found`
// and not `added == 0`. Both were verified against the live Trakt API on
// 2026-07-12 and both must stay successes:
//   - re-adding an item already present -> added:0, existing:1, not_found:[]
//   - removing an item that is not there -> deleted:0, not_found:[]
//
// In particular, Trakt's `not_found` on a remove means "could not resolve this
// id", NOT "this item was not in your list". If that ever changes, the
// idempotent-remove case below is what will catch it.
func TestMutationVerdict(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantApplied    int
		wantUnresolved int
	}{
		{
			name:           "idempotent re-add stays a success (existing counts as applied)",
			body:           `{"added":{"movies":0},"existing":{"movies":1},"not_found":{"movies":[]}}`,
			wantApplied:    1,
			wantUnresolved: 0,
		},
		{
			name:           "idempotent remove stays a success (absent item is not not_found)",
			body:           `{"deleted":{"movies":0},"not_found":{"movies":[],"shows":[],"people":[],"users":[]}}`,
			wantApplied:    0,
			wantUnresolved: 0,
		},
		{
			name:           "unresolvable id with nothing applied is NOT_APPLIED",
			body:           `{"deleted":{"movies":0},"not_found":{"movies":[{"ids":{"slug":"nope"}}]}}`,
			wantApplied:    0,
			wantUnresolved: 1,
		},
		{
			name:           "partial: one applied, one unresolved",
			body:           `{"added":{"movies":1},"not_found":{"movies":[{"ids":{"slug":"nope"}}]}}`,
			wantApplied:    1,
			wantUnresolved: 1,
		},
		{
			// Response shape confirmed live 2026-07-12: reordering a watchlist
			// with an unrecognized rank id returns exactly
			// {"updated":0,"skipped_ids":[999999999]} — reorder signals refusal
			// through skipped_ids, never through not_found.
			name:           "reorder with every id skipped is NOT_APPLIED",
			body:           `{"updated":0,"skipped_ids":[999999999]}`,
			wantApplied:    0,
			wantUnresolved: 1,
		},
		{
			name:           "reorder partially skipped is a partial success",
			body:           `{"updated":5,"skipped_ids":[9]}`,
			wantApplied:    5,
			wantUnresolved: 1,
		},
		{
			name:           "not_found spread across several media buckets",
			body:           `{"added":{"movies":0},"not_found":{"movies":[{"a":1}],"shows":[{"b":2},{"c":3}]}}`,
			wantApplied:    0,
			wantUnresolved: 3,
		},
		// Lenient decode: anything we do not model must fail toward success,
		// never invent a NOT_APPLIED on an endpoint we did not anticipate.
		{
			name:           "unmodeled object body (e.g. list-create) is a success",
			body:           `{"name":"My List","ids":{"trakt":123}}`,
			wantApplied:    0,
			wantUnresolved: 0,
		},
		{
			name:           "non-array not_found value is ignored, not counted",
			body:           `{"not_found":{"movies":"unexpected"}}`,
			wantApplied:    0,
			wantUnresolved: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b mutationBuckets
			if err := json.Unmarshal([]byte(tc.body), &b); err != nil {
				t.Fatalf("decoding test body: %v", err)
			}
			if got := b.applied(); got != tc.wantApplied {
				t.Errorf("applied() = %d, want %d", got, tc.wantApplied)
			}
			if got := b.unresolved(); got != tc.wantUnresolved {
				t.Errorf("unresolved() = %d, want %d", got, tc.wantUnresolved)
			}

			// The verdict itself: NOT_APPLIED requires a refusal AND no effect.
			gotNotApplied := b.unresolved() > 0 && b.applied() == 0
			wantNotApplied := tc.wantUnresolved > 0 && tc.wantApplied == 0
			if gotNotApplied != wantNotApplied {
				t.Errorf("NOT_APPLIED = %v, want %v", gotNotApplied, wantNotApplied)
			}
		})
	}
}

// An empty body (HTTP 204) carries no buckets and must never be read as a
// refusal — emitMutation skips the decode entirely, so this asserts the
// zero-value behaviour it relies on.
func TestMutationVerdictEmptyBody(t *testing.T) {
	var b mutationBuckets
	if b.applied() != 0 || b.unresolved() != 0 {
		t.Fatalf("zero-value buckets: applied=%d unresolved=%d, want 0/0",
			b.applied(), b.unresolved())
	}
	if b.unresolved() > 0 && b.applied() == 0 {
		t.Error("a 204 with no body must not be NOT_APPLIED")
	}
}
