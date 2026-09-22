package ipv6

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
		err  bool
	}{
		{"canonical compressed", "2001:db8::1", "2001:db8::1", false},
		{"full form normalized", "2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1", false},
		{"trimmed", "  2001:db8::1  ", "2001:db8::1", false},
		{"empty", "", "", true},
		{"bracketed", "[2001:db8::1]", "", true},
		{"zone", "fe80::1%eth0", "", true},
		{"ipv4 rejected", "10.0.0.1", "", true},
		{"ipv4-mapped rejected", "::ffff:10.0.0.1", "", true},
		{"dns rejected", "db.example.com", "", true},
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
	got, err := Addresses("2001:db8::1")
	if err != nil {
		t.Fatalf("Addresses err = %v", err)
	}
	if len(got) != 1 || got[0] != "2001:db8::1" {
		t.Fatalf("Addresses = %v, want [2001:db8::1]", got)
	}
}
