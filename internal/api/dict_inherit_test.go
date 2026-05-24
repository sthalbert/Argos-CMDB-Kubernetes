package api

import "testing"

func intp(n int) *int         { return &n }
func strptr(s string) *string { return &s }

func TestEffectiveDICT(t *testing.T) {
	t.Parallel()

	appWithDICT := &Application{
		SecDisponibilite:   intp(3),
		SecIntegrite:       intp(2),
		SecConfidentialite: intp(4),
		SecTracabilite:     intp(1),
		SecNotes:           strptr("classified by app"),
	}
	appNoDICT := &Application{} // linked but no axes

	tests := []struct {
		name           string
		fbD, fbI       *int
		fbC, fbT       *int
		fbNotes        *string
		fallbackSource DICTSource
		linkedApp      *Application
		wantSource     DICTSource
		wantD          *int // expected effective disponibilite
		wantNotes      *string
	}{
		{
			name:           "linked + app has DICT → application",
			fbD:            intp(0),
			fallbackSource: DICTSourceWorkload,
			linkedApp:      appWithDICT,
			wantSource:     DICTSourceApplication,
			wantD:          intp(3),
			wantNotes:      strptr("classified by app"),
		},
		{
			name:           "linked + app all-nil + workload has DICT → workload",
			fbD:            intp(2),
			fbNotes:        strptr("workload note"),
			fallbackSource: DICTSourceWorkload,
			linkedApp:      appNoDICT,
			wantSource:     DICTSourceWorkload,
			wantD:          intp(2),
			wantNotes:      strptr("workload note"),
		},
		{
			name:           "not linked + workload has DICT → workload",
			fbC:            intp(1),
			fallbackSource: DICTSourceWorkload,
			linkedApp:      nil,
			wantSource:     DICTSourceWorkload,
			wantD:          nil,
		},
		{
			name:           "not linked + nothing set → none",
			fallbackSource: DICTSourceWorkload,
			linkedApp:      nil,
			wantSource:     DICTSourceNone,
			wantD:          nil,
		},
		{
			name:           "VM: fallback all-nil + linked app has DICT → application",
			fallbackSource: DICTSourceWorkload, // VMs pass workload-ish source; never reached
			linkedApp:      appWithDICT,
			wantSource:     DICTSourceApplication,
			wantD:          intp(3),
			wantNotes:      strptr("classified by app"),
		},
		{
			name:           "VM: fallback all-nil + no link → none",
			fallbackSource: DICTSourceWorkload,
			linkedApp:      nil,
			wantSource:     DICTSourceNone,
			wantD:          nil,
		},
		{
			name:           "namespace fallback source is honoured",
			fbI:            intp(4),
			fallbackSource: DICTSourceNamespace,
			linkedApp:      nil,
			wantSource:     DICTSourceNamespace,
			wantD:          nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeEffectiveDICT(tc.fbD, tc.fbI, tc.fbC, tc.fbT, tc.fbNotes, tc.fallbackSource, tc.linkedApp)
			if got.Source != tc.wantSource {
				t.Fatalf("source = %q, want %q", got.Source, tc.wantSource)
			}
			if !intEq(got.Disponibilite, tc.wantD) {
				t.Errorf("disponibilite = %v, want %v", deref(got.Disponibilite), deref(tc.wantD))
			}
			if tc.wantNotes != nil {
				if got.Notes == nil || *got.Notes != *tc.wantNotes {
					t.Errorf("notes = %v, want %q", got.Notes, *tc.wantNotes)
				}
			}
			if tc.wantSource == DICTSourceNone {
				if anyAxisSet(got.Disponibilite, got.Integrite, got.Confidentialite, got.Tracabilite) {
					t.Errorf("source=none must carry no axes, got %+v", got)
				}
			}
		})
	}
}

func intEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
