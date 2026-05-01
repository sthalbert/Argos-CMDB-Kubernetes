package filter

import (
	"testing"

	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

func TestApply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tags map[string]string
		drop bool
	}{
		{"no tags", nil, false},
		{"unrelated tag", map[string]string{"env": "prod"}, false},
		{"longue-vue.io/ignore=true drops", map[string]string{"longue-vue.io/ignore": "true"}, true},
		{"longue-vue.io/ignore=false keeps", map[string]string{"longue-vue.io/ignore": "false"}, false},
		{"OscK8sNodeName drops", map[string]string{"OscK8sNodeName": "ip-10-0-0-1"}, true},
		{"OscK8sClusterID/<cluster> drops", map[string]string{"OscK8sClusterID/abc": "owned"}, true},
		{"ansible_group present keeps", map[string]string{"ansible_group": "vault"}, false},
		{"ansible_group absent keeps", map[string]string{"Name": "bastion"}, false},
		{"mix: ignore + ansible drops", map[string]string{"longue-vue.io/ignore": "true", "ansible_group": "vault"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vms := []provider.VM{{ProviderVMID: "i-1", Tags: tc.tags}}
			got := Apply(vms)
			if tc.drop && len(got) != 0 {
				t.Errorf("expected drop, got %d kept", len(got))
			}
			if !tc.drop && len(got) != 1 {
				t.Errorf("expected keep, got %d kept", len(got))
			}
		})
	}
}

func TestApplyPreservesUnrelatedVMs(t *testing.T) {
	t.Parallel()
	vms := []provider.VM{
		{ProviderVMID: "i-keep", Tags: map[string]string{"Name": "vault"}},
		{ProviderVMID: "i-drop", Tags: map[string]string{"OscK8sNodeName": "x"}},
		{ProviderVMID: "i-keep2", Tags: map[string]string{"ansible_group": "dns"}},
	}
	got := Apply(vms)
	if len(got) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(got))
	}
	if got[0].ProviderVMID != "i-keep" || got[1].ProviderVMID != "i-keep2" {
		t.Errorf("unexpected order: %+v", got)
	}
}
