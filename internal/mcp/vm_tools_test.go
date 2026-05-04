//nolint:goconst // duplicated literals in assertions are clearer than named constants.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestHandleListCloudAccounts_RedactsAccessKey(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ak1, ak2 := "AKIA0000PUBLIC0001", "AKIA0000PUBLIC0002"
	now := time.Now().UTC()
	store.accounts = []api.CloudAccount{
		{ID: uuid.New(), Provider: "outscale", Name: "prod-eu", Region: "eu-west-2", Status: api.CloudAccountStatusActive, AccessKey: &ak1, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), Provider: "outscale", Name: "prod-us", Region: "us-east-2", Status: api.CloudAccountStatusActive, AccessKey: &ak2, CreatedAt: now, UpdatedAt: now},
	}
	s := newServer(t, store)

	r, err := s.handleListCloudAccounts(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := resultText(t, r)
	if strings.Contains(text, "access_key") {
		t.Errorf("response contains access_key field: %s", text)
	}
	if strings.Contains(text, ak1) || strings.Contains(text, ak2) {
		t.Errorf("response leaks access key value")
	}
	var got []api.CloudAccount
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d; want 2", len(got))
	}
	for i := range got {
		if got[i].AccessKey != nil {
			t.Errorf("account %d AccessKey not redacted", i)
		}
	}
}
