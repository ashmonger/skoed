//go:build !linux

package dhcp

import "fmt"

type stubProvider struct{}

func (p *stubProvider) Dump() (map[string]NeighEntry, error) {
	return nil, fmt.Errorf("operation not permitted (non-linux build)")
}

func NewNeighborProvider() NeighborProvider {
	return &stubProvider{}
}
