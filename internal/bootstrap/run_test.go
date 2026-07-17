package bootstrap

import "testing"

func TestLocalRuntimeURL(t *testing.T) {
	if got := localRuntimeURL("0.0.0.0", 10789); got != "http://127.0.0.1:10789" {
		t.Fatalf("wildcard host URL = %q", got)
	}
	if got := localRuntimeURL("::1", 10789); got != "http://[::1]:10789" {
		t.Fatalf("IPv6 URL = %q", got)
	}
}
