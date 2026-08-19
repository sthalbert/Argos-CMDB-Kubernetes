package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestNamePattern(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain term wraps as substring", "du", "%du%"},
		{"uppercase folds to lower", "Du", "%du%"},
		{"trailing star anchors prefix", "du*", "du%"},
		{"leading star anchors suffix", "*du", "%du"},
		{"middle star anchors both ends", "prod-*-db", "prod-%-db"},
		{"underscore is literal", "my_app", `%my\_app%`},
		{"percent is literal", "50%", `%50\%%`},
		{"backslash is literal", `a\b`, `%a\\b%`},
		{"star with metachars", "my_app*", `my\_app%`},
		{"lone star matches everything", "*", "%"},
	}
	for _, tc := range cases {
		if got := namePattern(tc.in); got != tc.want {
			t.Errorf("%s: namePattern(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestListCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	val := "widget-7"
	enc := encodeListCursor("name", &val, id, dirAsc)

	gotVal, gotID, err := decodeListCursor(enc, "name", dirAsc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotVal == nil || *gotVal != val {
		t.Errorf("val = %v, want %q", gotVal, val)
	}
	if gotID != id {
		t.Errorf("id = %v, want %v", gotID, id)
	}
}

func TestListCursorNullVal(t *testing.T) {
	id := uuid.New()
	enc := encodeListCursor("owner", nil, id, dirAsc)
	gotVal, gotID, err := decodeListCursor(enc, "owner", dirAsc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotVal != nil {
		t.Errorf("val = %q, want nil", *gotVal)
	}
	if gotID != id {
		t.Errorf("id = %v, want %v", gotID, id)
	}
}

func TestListCursorRejectsMismatchAndGarbage(t *testing.T) {
	id := uuid.New()
	val := "x"
	enc := encodeListCursor("name", &val, id, dirAsc)

	cases := []struct {
		name             string
		cursor           string
		wantCol, wantDir string
	}{
		{"different sort column", enc, "created_at", dirAsc},
		{"different direction", enc, "name", dirDesc},
		{"garbage", "not-base64!!", "name", dirAsc},
		{"legacy pipe cursor", encodeCursor(timeNowFixed(t), id), "created_at", dirDesc},
		{"valid b64, not json", "aGVsbG8", "name", dirAsc},
	}
	for _, tc := range cases {
		_, _, err := decodeListCursor(tc.cursor, tc.wantCol, tc.wantDir)
		if !errors.Is(err, api.ErrInvalidCursor) {
			t.Errorf("%s: err = %v, want api.ErrInvalidCursor", tc.name, err)
		}
	}
}

// timeNowFixed returns a fixed timestamp for legacy-cursor construction.
func timeNowFixed(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

var testSpec = sortSpec{
	columns: map[string]sortColumn{
		"created_at": {expr: "n.created_at", kind: sortTime},
		"name":       {expr: "LOWER(n.name)", kind: sortText},
		"zone":       {expr: "LOWER(n.zone)", kind: sortText, nullable: true},
		"priority":   {expr: "n.priority", kind: sortInt, nullable: true},
	},
	defaultKey: "created_at",
}

//nolint:gocyclo // sequential assertion script; no real branching
func TestSortSpecResolve(t *testing.T) {
	// No sort → default key, desc (historical order preserved).
	key, _, dir, err := testSpec.resolve(api.ListPage{})
	if err != nil || key != "created_at" || dir != dirDesc {
		t.Fatalf("default: key=%q dir=%q err=%v, want created_at/desc/nil", key, dir, err)
	}
	// Explicit sort, no order → asc.
	key, _, dir, err = testSpec.resolve(api.ListPage{Sort: "name"})
	if err != nil || key != "name" || dir != dirAsc {
		t.Fatalf("sort=name: key=%q dir=%q err=%v, want name/asc/nil", key, dir, err)
	}
	// Explicit desc honored.
	_, _, dir, err = testSpec.resolve(api.ListPage{Sort: "name", Order: dirDesc})
	if err != nil || dir != dirDesc {
		t.Fatalf("order=desc: dir=%q err=%v", dir, err)
	}
	// Unknown key / bad order → ErrInvalidSort.
	if _, _, _, err = testSpec.resolve(api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: err=%v, want ErrInvalidSort", err)
	}
	if _, _, _, err = testSpec.resolve(api.ListPage{Sort: "name", Order: "sideways"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bad order: err=%v, want ErrInvalidSort", err)
	}
	// order without sort is ignored — historical order preserved.
	key, _, dir, err = testSpec.resolve(api.ListPage{Order: dirAsc})
	if err != nil || key != "created_at" || dir != dirDesc {
		t.Fatalf("order-no-sort: key=%q dir=%q err=%v, want created_at/desc/nil", key, dir, err)
	}
}

func TestOrderBy(t *testing.T) {
	cases := []struct {
		col  sortColumn
		dir  string
		want string
	}{
		{testSpec.columns["created_at"], dirDesc, "ORDER BY n.created_at DESC, n.id DESC"},
		{testSpec.columns["name"], dirAsc, "ORDER BY LOWER(n.name) ASC, n.id ASC"},
		{testSpec.columns["zone"], dirAsc, "ORDER BY LOWER(n.zone) ASC NULLS LAST, n.id ASC"},
		{testSpec.columns["zone"], dirDesc, "ORDER BY LOWER(n.zone) DESC NULLS LAST, n.id DESC"},
	}
	for _, tc := range cases {
		if got := orderBy(tc.col, "n.id", tc.dir); got != tc.want {
			t.Errorf("orderBy(%v,%s) = %q, want %q", tc.col, tc.dir, got, tc.want)
		}
	}
}

//nolint:gocyclo // sequential assertion script over the predicate matrix; no real branching
func TestKeysetCond(t *testing.T) {
	id := uuid.New()
	v := "widget"
	ts := "2026-01-02T03:04:05.000000006Z"

	// Non-nullable text asc → row-value comparison, 2 args (string, uuid).
	conds, args := []string{}, []any{}
	if err := keysetCond(testSpec.columns["name"], "n.id", dirAsc, &v, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	if len(conds) != 1 || conds[0] != "(LOWER(n.name), n.id) > ($1, $2)" {
		t.Errorf("text asc cond = %v", conds)
	}
	if len(args) != 2 || args[0] != "widget" {
		t.Errorf("text asc args = %v", args)
	}

	// Non-nullable time desc → row-value <, arg parsed to time.Time.
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["created_at"], "n.id", dirDesc, &ts, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	if conds[0] != "(n.created_at, n.id) < ($1, $2)" {
		t.Errorf("time desc cond = %v", conds)
	}
	if _, ok := args[0].(time.Time); !ok {
		t.Errorf("time arg not parsed: %T", args[0])
	}

	// Nullable asc with value → OR-form including IS NULL region.
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["zone"], "n.id", dirAsc, &v, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	want := "(LOWER(n.zone) > $1 OR (LOWER(n.zone) = $1 AND n.id > $2) OR LOWER(n.zone) IS NULL)"
	if conds[0] != want {
		t.Errorf("nullable asc cond = %q, want %q", conds[0], want)
	}

	// Nullable, cursor inside the NULL region (val nil).
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["zone"], "n.id", dirAsc, nil, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	if conds[0] != "(LOWER(n.zone) IS NULL AND n.id > $1)" {
		t.Errorf("null-region cond = %q", conds[0])
	}
	if len(args) != 1 {
		t.Errorf("null-region args = %v", args)
	}

	// Bad time value in cursor → ErrInvalidCursor.
	bad := "not-a-time"
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["created_at"], "n.id", dirAsc, &bad, id, &conds, &args); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("bad time: err=%v, want ErrInvalidCursor", err)
	}

	// Nil val on a non-nullable column → corrupt cursor.
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["name"], "n.id", dirAsc, nil, id, &conds, &args); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("nil val non-nullable: err=%v, want ErrInvalidCursor", err)
	}

	// Nullable desc with value → OR-form with < and IS NULL tail.
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["zone"], "n.id", dirDesc, &v, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	wantDesc := "(LOWER(n.zone) < $1 OR (LOWER(n.zone) = $1 AND n.id < $2) OR LOWER(n.zone) IS NULL)"
	if conds[0] != wantDesc {
		t.Errorf("nullable desc cond = %q, want %q", conds[0], wantDesc)
	}

	// Non-nullable int asc → row-value comparison, arg parsed as int.
	// (We temporarily make priority non-nullable for this sub-test by
	// constructing a local sortColumn.)
	intCol := sortColumn{expr: "n.priority", kind: sortInt}
	intVal := "7"
	conds, args = []string{}, []any{}
	if err := keysetCond(intCol, "n.id", dirAsc, &intVal, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	if conds[0] != "(n.priority, n.id) > ($1, $2)" {
		t.Errorf("int asc cond = %v", conds)
	}
	if len(args) != 2 || args[0] != 7 {
		t.Errorf("int asc args = %v, want [7, uuid]", args)
	}

	// Nullable int with value → OR-form including IS NULL.
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["priority"], "n.id", dirAsc, &intVal, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	wantInt := "(n.priority > $1 OR (n.priority = $1 AND n.id > $2) OR n.priority IS NULL)"
	if conds[0] != wantInt {
		t.Errorf("nullable int asc cond = %q, want %q", conds[0], wantInt)
	}

	// Bad int value in cursor → ErrInvalidCursor.
	badInt := "not-an-int"
	conds, args = []string{}, []any{}
	if err := keysetCond(intCol, "n.id", dirAsc, &badInt, id, &conds, &args); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("bad int: err=%v, want ErrInvalidCursor", err)
	}

	// Nullable int, cursor in NULL region (val nil).
	conds, args = []string{}, []any{}
	if err := keysetCond(testSpec.columns["priority"], "n.id", dirAsc, nil, id, &conds, &args); err != nil {
		t.Fatal(err)
	}
	if conds[0] != "(n.priority IS NULL AND n.id > $1)" {
		t.Errorf("nullable int null-region cond = %q", conds[0])
	}
	if len(args) != 1 {
		t.Errorf("nullable int null-region args = %v, want 1", args)
	}
}

func TestClampLimit(t *testing.T) {
	if got := clampLimit(0, 200); got != 50 {
		t.Errorf("clampLimit(0)=%d want 50", got)
	}
	if got := clampLimit(-3, 200); got != 50 {
		t.Errorf("clampLimit(-3)=%d want 50", got)
	}
	if got := clampLimit(999, 200); got != 200 {
		t.Errorf("clampLimit(999)=%d want 200", got)
	}
	if got := clampLimit(120, 500); got != 120 {
		t.Errorf("clampLimit(120,500)=%d want 120", got)
	}
}

func TestSortVals(t *testing.T) {
	if got := sortValText(nil); got != nil {
		t.Errorf("sortValText(nil) = %v, want nil", got)
	}
	s := "MiXeD"
	if got := sortValText(&s); got == nil || *got != "mixed" {
		t.Errorf("sortValText = %v, want mixed", got)
	}
	if got := sortValTime(nil); got != nil {
		t.Errorf("sortValTime(nil) = %v, want nil", got)
	}
	ts, err := time.Parse(time.RFC3339Nano, "2026-01-02T03:04:05.000000006Z")
	if err != nil {
		t.Fatal(err)
	}
	if got := sortValTime(&ts); got == nil || *got != "2026-01-02T03:04:05.000000006Z" {
		t.Errorf("sortValTime = %v, want RFC3339Nano string", got)
	}
}

func TestSortValInt(t *testing.T) {
	if got := sortValInt(nil); got != nil {
		t.Errorf("sortValInt(nil) = %v, want nil", got)
	}
	n := 42
	if got := sortValInt(&n); got == nil || *got != "42" {
		t.Errorf("sortValInt = %v, want 42", got)
	}
}

func TestBoolToIntPtr(t *testing.T) {
	if got := boolToIntPtr(nil); got != nil {
		t.Errorf("boolToIntPtr(nil) = %v, want nil", got)
	}
	tBool := true
	if got := boolToIntPtr(&tBool); got == nil || *got != 1 {
		t.Errorf("boolToIntPtr(true) = %v, want 1", got)
	}
	fBool := false
	if got := boolToIntPtr(&fBool); got == nil || *got != 0 {
		t.Errorf("boolToIntPtr(false) = %v, want 0", got)
	}
}

func TestSeverityRank(t *testing.T) {
	if got := severityRank(nil); got == nil || *got != -1 {
		t.Errorf("severityRank(nil) = %v, want -1", got)
	}
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"critical", 4},
		{"high", 3},
		{"medium", 2},
		{"low", 1},
		{"info", 0},
		{"unknown", -1},
		{"", -1},
		{"CRITICAL", 4},
		{"High", 3},
	} {
		got := severityRank(&tc.in)
		if got == nil || *got != tc.want {
			t.Errorf("severityRank(%q) = %v, want %d", tc.in, got, tc.want)
		}
	}
}
