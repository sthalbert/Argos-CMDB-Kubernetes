package api

import (
	"testing"
)

func TestUpsertOutcome_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		o    UpsertOutcome
		want string
	}{
		{OutcomeInserted, "inserted"},
		{OutcomeBusinessChanged, "business_changed"},
		{OutcomeNoChange, "no_change"},
		{UpsertOutcome(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("UpsertOutcome(%d).String() = %q, want %q", tc.o, got, tc.want)
		}
	}
}
