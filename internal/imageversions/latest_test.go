package imageversions

import "testing"

func TestComputeLatest(t *testing.T) {
	tags := []string{
		"latest", "stable", "master",
		"1.24.0", "1.25.0", "1.25.3", "1.27.0", "1.27.4",
		"1.27.5-rc1",
		"1.25.3-alpine", "1.27.0-alpine", "1.27.4-alpine",
		"1.25.3-debian-12", "1.27.4-debian-12",
		"sha-abc123",
	}

	cases := []struct {
		name    string
		variant string
		want    string
	}{
		{"pure semver", "", "1.27.4"},
		{"alpine variant", "alpine", "1.27.4-alpine"},
		{"debian-12 variant", "debian-12", "1.27.4-debian-12"},
		{"unknown variant", "windows-server-core", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComputeLatest(tc.variant, tags)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("variant=%q: want %q, got %q", tc.variant, tc.want, got)
			}
		})
	}
}

func TestComputeLatest_EmptyTags(t *testing.T) {
	got, err := ComputeLatest("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty result, got %q", got)
	}
}

func TestComputeLatest_AllPrerelease(t *testing.T) {
	got, _ := ComputeLatest("", []string{"1.0.0-rc1", "1.0.0-rc2", "1.0.0-beta"})
	if got != "" {
		t.Errorf("expected empty when only prereleases, got %q", got)
	}
}

func TestComputeLatest_LargeTagList(t *testing.T) {
	// Stress: many tags, only some match the variant.
	var tags []string
	for i := 1; i <= 100; i++ {
		// Add semver and alpine variant for each
		tags = append(tags, "1.0."+itoa(i))
		if i%3 == 0 {
			tags = append(tags, "1.0."+itoa(i)+"-alpine")
		}
	}
	// Pick the highest matching alpine: should be the largest i divisible by 3 and <=100, i.e. 99.
	got, err := ComputeLatest("alpine", tags)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "1.0.99-alpine" {
		t.Errorf("expected 1.0.99-alpine, got %q", got)
	}
}

// tiny non-allocating int-to-string for the stress test (avoids strconv import).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
