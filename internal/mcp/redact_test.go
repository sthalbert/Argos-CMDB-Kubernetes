package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

//nolint:gocyclo // sequential field assertions are clearer than a table-driven split.
func TestRedactCloudAccount_NilsAccessKey(t *testing.T) {
	t.Parallel()
	ak := "AKIAEXAMPLEPUBLICID"
	owner := "team-a"
	now := time.Now().UTC()
	in := api.CloudAccount{
		ID:          uuid.New(),
		Provider:    "outscale",
		Name:        "prod-eu",
		Region:      "eu-west-2",
		Status:      api.CloudAccountStatusActive,
		AccessKey:   &ak,
		Owner:       &owner,
		Annotations: map[string]string{"team": "platform"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	out := redactCloudAccount(in)

	if out.AccessKey != nil {
		t.Errorf("AccessKey = %q; want nil", *out.AccessKey)
	}
	// Every other field must be preserved.
	if out.ID != in.ID || out.Provider != in.Provider || out.Name != in.Name ||
		out.Region != in.Region || out.Status != in.Status {
		t.Errorf("identity fields mutated: %+v", out)
	}
	if out.Owner == nil || *out.Owner != owner {
		t.Errorf("Owner mutated: %v", out.Owner)
	}
	if out.Annotations["team"] != "platform" {
		t.Errorf("annotations mutated: %v", out.Annotations)
	}

	// Original input must not be mutated (value receiver semantics).
	if in.AccessKey == nil {
		t.Error("input AccessKey was mutated; redactCloudAccount must operate on a copy")
	}

	// Round-trip JSON must not contain access_key at all.
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "access_key") {
		t.Errorf("marshalled JSON contains access_key: %s", data)
	}
}
