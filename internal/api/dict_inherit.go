package api

// DICTSource names where an entity's effective DICT classification came from.
// See ADR-0029 §6 for the precedence rules.
type DICTSource string

const (
	// DICTSourceApplication — the value was inherited from the linked
	// Application's DICT (the highest-precedence source).
	DICTSourceApplication DICTSource = "application"
	// DICTSourceWorkload — the value came from the workload's own DICT
	// columns (fallback when the entity is unlinked or the linked
	// application carries no DICT axis).
	DICTSourceWorkload DICTSource = "workload"
	// DICTSourceNamespace — symmetric fallback for namespace-scoped
	// inheritance.
	DICTSourceNamespace DICTSource = "namespace"
	// DICTSourceNone — neither the linked application nor the entity itself
	// carries any DICT axis.
	DICTSourceNone DICTSource = "none"
)

// EffectiveDICT is the read-only inherited classification shipped on
// workload + VM responses (ADR-0029 §6). It is computed at read time and is
// never accepted on PATCH/POST bodies.
//
// This struct is hand-written and excluded from oapi-codegen (see
// api/openapi/oapi-codegen.yaml) so the generated `Workload.EffectiveDict`
// field references it without codegen emitting a parallel struct.
type EffectiveDICT struct {
	Disponibilite   *int       `json:"disponibilite,omitempty"`
	Integrite       *int       `json:"integrite,omitempty"`
	Confidentialite *int       `json:"confidentialite,omitempty"`
	Tracabilite     *int       `json:"tracabilite,omitempty"`
	Notes           *string    `json:"notes,omitempty"`
	Source          DICTSource `json:"source"`
}

// anyAxisSet reports whether at least one of the four DICT axes is non-nil.
func anyAxisSet(d, i, c, t *int) bool { return d != nil || i != nil || c != nil || t != nil }

// applicationDICT returns the four axes + notes carried by an application.
func applicationDICT(app *Application) (d, i, c, t *int, notes *string) {
	return app.SecDisponibilite, app.SecIntegrite, app.SecConfidentialite, app.SecTracabilite, app.SecNotes
}

// ComputeEffectiveDICT computes inherited classification per ADR-0029 §6.
// The `fb*` args are the entity's own DICT axes (workload/namespace); pass
// all-nil for VMs, which have no DICT columns of their own. `fallbackSource`
// is DICTSourceWorkload or DICTSourceNamespace. `linkedApp` may be nil
// (unlinked).
//
// Precedence:
//  1. linked AND the application has any axis set → application's DICT.
//  2. else the entity's own DICT has any axis set → fallbackSource.
//  3. else → none.
func ComputeEffectiveDICT(
	fbD, fbI, fbC, fbT *int, fbNotes *string, fallbackSource DICTSource,
	linkedApp *Application,
) EffectiveDICT {
	if linkedApp != nil {
		ad, ai, ac, at, an := applicationDICT(linkedApp)
		if anyAxisSet(ad, ai, ac, at) {
			return EffectiveDICT{
				Disponibilite: ad, Integrite: ai, Confidentialite: ac, Tracabilite: at,
				Notes: an, Source: DICTSourceApplication,
			}
		}
	}
	if anyAxisSet(fbD, fbI, fbC, fbT) {
		return EffectiveDICT{
			Disponibilite: fbD, Integrite: fbI, Confidentialite: fbC, Tracabilite: fbT,
			Notes: fbNotes, Source: fallbackSource,
		}
	}
	return EffectiveDICT{Source: DICTSourceNone}
}
