package mountlocal

import "testing"

func TestHostsMapping(t *testing.T) {
	for _, tc := range []struct {
		name, text  string
		found, fail bool
	}{
		{"missing", "127.0.0.1 localhost\n", false, false},
		{"exact", "127.0.0.1 TDrive.localhost # TDrive\n", true, false},
		{"alias and case", "127.0.0.1 localhost tdrive.LOCALHOST\r\n", true, false},
		{"comment", "# 127.0.0.1 TDrive.localhost\n", false, false},
		{"remote", "192.0.2.1 TDrive.localhost\n", false, true},
		{"ipv6", "::1 TDrive.localhost\n", false, true},
		{"mixed", "127.0.0.1 TDrive.localhost\n192.0.2.1 tdrive.localhost\n", false, true},
		{"trailing dot conflict", "192.0.2.1 TDrive.localhost.\n", false, true},
		{"similar", "192.0.2.1 not-TDrive.localhost\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hostsMapping([]byte(tc.text))
			if (err != nil) != tc.fail || got != tc.found {
				t.Fatalf("mapping = %v, %v", got, err)
			}
		})
	}
}
