package config

import (
	"net/netip"
	"testing"
)

func TestParseProxies(t *testing.T) {
	ps, err := parseProxies("private, 203.0.113.7, 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	contains := func(addr string) bool {
		a := netip.MustParseAddr(addr)
		for _, p := range ps {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "172.17.0.1": true, "192.168.1.10": true,
		"203.0.113.7": true, "2001:db8::1": true,
		"203.0.113.8": false, "8.8.8.8": false,
	} {
		if got := contains(addr); got != want {
			t.Errorf("%s trusted = %v, want %v", addr, got, want)
		}
	}
	if ps, _ := parseProxies("none"); len(ps) != 0 {
		t.Errorf("none should trust nobody, got %v", ps)
	}
	if _, err := parseProxies("not-an-ip"); err == nil {
		t.Error("an invalid entry should be refused")
	}
}
