package provider

import "testing"

func TestCanonicalPowerState(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"pending":       "pending",
		"running":       "running",
		"stopping":      "stopping",
		"stopped":       "stopped",
		"shutting-down": "terminating",
		"terminated":    "terminated",
		"":              "unknown",
		"weird":         "weird",
	}
	for input, want := range tests {
		if got := CanonicalPowerState(input); got != want {
			t.Errorf("CanonicalPowerState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseInstanceTypeCapacity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantCPU string
		wantMem string
	}{
		{"tinav7.c4r8p2", "4", "8Gi"},
		{"tinav5.c2r4p1", "2", "4Gi"},
		{"tinav3.c16r32p3", "16", "32Gi"},
		{"m5.large", "", ""},
		{"unknown", "", ""},
		{"", "", ""},
	}
	for _, tc := range tests {
		gotCPU, gotMem := ParseInstanceTypeCapacity(tc.in)
		if gotCPU != tc.wantCPU || gotMem != tc.wantMem {
			t.Errorf("Parse(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotCPU, gotMem, tc.wantCPU, tc.wantMem)
		}
	}
}

func TestDeriveRegionFromZone(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"eu-west-2b":           "eu-west-2",
		"eu-west-2a":           "eu-west-2",
		"us-east-1c":           "us-east-1",
		"cloudgouv-eu-west-1a": "cloudgouv-eu-west-1",
		"":                     "",
		"weird":                "",
		"eu-west-2":            "", // already a region, no zone suffix
	}
	for in, want := range tests {
		if got := DeriveRegionFromZone(in); got != want {
			t.Errorf("DeriveRegionFromZone(%q) = %q, want %q", in, got, want)
		}
	}
}
