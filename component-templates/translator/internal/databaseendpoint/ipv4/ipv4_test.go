package ipv4

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
		err  bool
	}{
		{"canonical", "10.0.0.1", "10.0.0.1", false},
		{"leading zero stripped", "010.0.0.1", "", true}, // netip rejects leading zeros
		{"trimmed", "  10.0.0.1  ", "10.0.0.1", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"bracketed", "[10.0.0.1]", "", true},
		{"trailing bracket", "10.0.0.1]", "", true},
		{"zone", "10.0.0.1%eth0", "", true},
		{"ipv6 rejected", "2001:db8::1", "", true},
		{"dns rejected", "db.example.com", "", true},
		{"hostname with dot rejected", "10.0.0.1.", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.host)
			if tc.err {
				if err == nil {
					t.Fatalf("Normalize(%q) = %q, want error", tc.host, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q) err = %v", tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestAddresses(t *testing.T) {
	got, err := Addresses("10.0.0.1")
	if err != nil {
		t.Fatalf("Addresses err = %v", err)
	}
	if len(got) != 1 || got[0] != "10.0.0.1" {
		t.Fatalf("Addresses = %v, want [10.0.0.1]", got)
	}
}
