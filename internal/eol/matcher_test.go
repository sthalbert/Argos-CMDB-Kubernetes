package eol

import "testing"

func TestKubernetesMatcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input   string
		wantOK  bool
		product string
		cycle   string
	}{
		{"v1.28.5", true, "kubernetes", "1.28"},
		{"1.28.5", true, "kubernetes", "1.28"},
		{"v1.30.0-rc.1", true, "kubernetes", "1.30"},
		{"", false, "", ""},
		{"garbage", false, "", ""},
	}

	m := KubernetesMatcher{}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, ok := m.Match(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("Match(%q) ok=%v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && (got.Product != tt.product || got.Cycle != tt.cycle) {
				t.Errorf("Match(%q) = {%s, %s}, want {%s, %s}", tt.input, got.Product, got.Cycle, tt.product, tt.cycle)
			}
		})
	}
}

func TestContainerRuntimeMatcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input   string
		wantOK  bool
		product string
		cycle   string
	}{
		{"containerd://1.7.2", true, "containerd", "1.7"},
		{"containerd://v1.6.28", true, "containerd", "1.6"},
		{"cri-o://1.28.0", true, "cri-o", "1.28"},
		{"docker://24.0.7", true, "docker", "24.0"},
		{"", false, "", ""},
		{"unknown", false, "", ""},
	}

	m := ContainerRuntimeMatcher{}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, ok := m.Match(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("Match(%q) ok=%v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && (got.Product != tt.product || got.Cycle != tt.cycle) {
				t.Errorf("Match(%q) = {%s, %s}, want {%s, %s}", tt.input, got.Product, got.Cycle, tt.product, tt.cycle)
			}
		})
	}
}

func TestOSImageMatcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input   string
		wantOK  bool
		product string
		cycle   string
	}{
		{"Ubuntu 22.04.3 LTS", true, "ubuntu", "22.04"},
		{"ubuntu 20.04", true, "ubuntu", "20.04"},
		{"Debian GNU/Linux 12 (bookworm)", true, "debian", "12"},
		{"Alpine Linux v3.18.4", true, "alpine", "3.18"},
		{"Red Hat Enterprise Linux 9.2", true, "rhel", "9"},
		{"Rocky Linux 9.3 (Blue Onyx)", true, "rocky-linux", "9"},
		{"AlmaLinux 8.8", true, "alma-linux", "8"},
		{"Amazon Linux 2", true, "amazon-linux", "2"},
		{"CentOS Linux 7 (Core)", true, "centos", "7"},
		{"Fedora 39", true, "fedora", "39"},
		{"Flatcar Container Linux 3510.2.1", true, "flatcar", "3510"},
		{"", false, "", ""},
		{"custom-image-v1", false, "", ""},
	}

	m := OSImageMatcher{}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, ok := m.Match(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("Match(%q) ok=%v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && (got.Product != tt.product || got.Cycle != tt.cycle) {
				t.Errorf("Match(%q) = {%s, %s}, want {%s, %s}", tt.input, got.Product, got.Cycle, tt.product, tt.cycle)
			}
		})
	}
}

func TestKernelMatcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input   string
		wantOK  bool
		product string
		cycle   string
	}{
		{"5.15.0-91-generic", true, "linux", "5.15"},
		{"6.1.0", true, "linux", "6.1"},
		{"4.18.0-477.13.1.el8_8.x86_64", true, "linux", "4.18"},
		{"", false, "", ""},
		{"not-a-kernel", false, "", ""},
	}

	m := KernelMatcher{}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, ok := m.Match(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("Match(%q) ok=%v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && (got.Product != tt.product || got.Cycle != tt.cycle) {
				t.Errorf("Match(%q) = {%s, %s}, want {%s, %s}", tt.input, got.Product, got.Cycle, tt.product, tt.cycle)
			}
		})
	}
}
