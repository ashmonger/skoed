package config_test

import (
    "testing"
    "github.com/skoed/skoed/internal/config"
)

func TestNormalise(t *testing.T) {
    cases := []struct{in, want string}{
        {"tls://1.1.1.1:853", "tls://1.1.1.1:853"},
        {"tls://1.1.1.1:853?skip_verify=true", "tls://1.1.1.1:853?skip_verify=true"},
        {"tls://1.1.1.1", "tls://1.1.1.1:853"},
        {"https://cloudflare-dns.com/dns-query", "https://cloudflare-dns.com/dns-query"},
        {"9.9.9.9:53", "9.9.9.9:53"},
        {"9.9.9.9", "9.9.9.9:53"},
    }
    for _, c := range cases {
        got, err := config.NormaliseUpstream(c.in)
        if err != nil { t.Errorf("%q: unexpected error: %v", c.in, err) }
        if got != c.want { t.Errorf("%q -> got %q, want %q", c.in, got, c.want) }
    }
}
