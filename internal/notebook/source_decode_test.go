package notebook

import (
	"testing"
)

func TestNotebookReadySourcesModernAndLegacyLayouts(t *testing.T) {
	cases := []struct {
		name      string
		source    any
		wantID    string
		wantReady bool
	}{
		{name: "regular ready", source: []any{[]any{"web-id"}, "Web source", nil, []any{nil, float64(2)}}, wantID: "web-id", wantReady: true},
		{name: "Drive backed ready", source: []any{[]any{nil, nil, []any{"drive-id"}}, "Google Drive", nil, []any{nil, float64(2)}}, wantID: "drive-id", wantReady: true},
		{name: "older source without status", source: []any{[]any{"legacy-id"}, "Legacy", nil}, wantID: "legacy-id", wantReady: true},
		{name: "plain ID", source: []any{"plain-id", "Plain source", nil, []any{nil, float64(2)}}, wantID: "plain-id", wantReady: true},
		{name: "processing", source: []any{[]any{"waiting-id"}, "Waiting", nil, []any{nil, float64(1)}}, wantReady: false},
		{name: "preparing", source: []any{[]any{"preparing-id"}, "Preparing", nil, []any{nil, float64(5)}}, wantReady: false},
		{name: "failed", source: []any{[]any{"failed-id"}, "Error", nil, []any{nil, float64(3)}}, wantReady: false},
		{name: "deleted", source: []any{[]any{"deleted-id"}, "Deleted", nil, []any{nil, float64(4)}}, wantReady: false},
		{name: "unknown status", source: []any{[]any{"unknown-id"}, "Unknown", nil, []any{nil, float64(99)}}, wantReady: false},
		{name: "unrecognized ID", source: []any{[]any{nil, nil}, "Broken", nil, []any{nil, float64(2)}}, wantReady: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := []any{nil, []any{tc.source}}
			ids, titles, counts := notebookReadySources(row)
			if tc.wantReady {
				if len(ids) != 1 || counts.ready != 1 {
					t.Fatalf("ready source rejected: ids=%#v stats=%+v", ids, counts)
				}
				got := str(at(ids[0], 0, 0))
				if got != tc.wantID {
					t.Fatalf("source ID: got %q want %q", got, tc.wantID)
				}
				if titles[tc.wantID] == "" {
					t.Fatalf("missing source title for %q", tc.wantID)
				}
			} else if len(ids) != 0 || counts.ready != 0 {
				t.Fatalf("unready source accepted: ids=%#v stats=%+v", ids, counts)
			}
		})
	}
}
func TestNotebookReadySourcesDetailedCounters(t *testing.T) {
	row := []any{nil, []any{
		[]any{[]any{"processing"}, "Processing", nil, []any{nil, float64(1)}},
		[]any{[]any{"failed"}, "Failed", nil, []any{nil, float64(3)}},
		[]any{[]any{"unknown"}, "Unknown", nil, []any{nil, float64(0)}},
		[]any{[]any{nil, nil}, "Bad ID", nil, []any{nil, float64(2)}},
	}}
	ids, _, stats := notebookReadySources(row)
	if len(ids) != 0 || stats.total != 4 || stats.processing != 1 || stats.failed != 1 || stats.unknown != 1 || stats.invalidIDs != 1 {
		t.Fatalf("wrong source status summary: ids=%#v stats=%+v", ids, stats)
	}
}
