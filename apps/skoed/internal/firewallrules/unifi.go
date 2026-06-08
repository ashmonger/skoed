package firewallrules

import (
	"encoding/json"
)

// UnifiRenderer emits a JSON document compatible with the UniFi
// controller's firewall ruleset payload. The body is text/plain
// (the JSON is the payload string). scope=all sets src_address_group
// to "any". action=reject sets action="reject".
type UnifiRenderer struct{}

type unifiAddrGroup struct {
	Type    string   `json:"type"`
	Value   string   `json:"value,omitempty"`
	Name    string   `json:"name,omitempty"`
	Members []string `json:"members,omitempty"`
}

type unifiPayload struct {
	Comment         string         `json:"_comment"`
	Name            string         `json:"name"`
	Ruleset         string         `json:"ruleset"`
	Action          string         `json:"action"`
	SrcAddressGroup unifiAddrGroup `json:"src_address_group"`
	DstAddressGroup unifiAddrGroup `json:"dst_address_group"`
	Provenance      unifiHeader    `json:"_provenance"`
}

// unifiHeader carries the same provenance fields as the comment block
// in the other renderers — UniFi has no native comment syntax inside
// a JSON ruleset, so we embed the data verbatim.
type unifiHeader struct {
	SnapshotID      string `json:"snapshot_id"`
	SnapshotFetched string `json:"snapshot_fetched"`
	ResolverCount   int    `json:"resolver_count"`
	GeneratedAt     string `json:"generated_at"`
	Scope           string `json:"scope"`
	StaleWarning    string `json:"warning,omitempty"`
}

func (UnifiRenderer) Render(p Plan) string {
	action := "drop"
	if p.Action == ActionReject {
		action = "reject"
	}

	src := unifiAddrGroup{Type: "address-group", Value: "any"}
	switch {
	case len(p.Sources) == 1:
		src.Value = p.Sources[0]
	case len(p.Sources) > 1:
		src.Type = "address-group"
		src.Name = "skoed_fw_sources"
		src.Members = append([]string(nil), p.Sources...)
		src.Value = ""
	}

	var members []string
	for _, r := range p.Resolvers {
		members = append(members, r.IPv4...)
	}
	for _, r := range p.Resolvers {
		members = append(members, r.IPv6...)
	}

	prov := unifiHeader{
		SnapshotID:      p.Snapshot.ID,
		SnapshotFetched: p.Snapshot.FetchedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ResolverCount:   p.Snapshot.ResolverCount,
		GeneratedAt:     p.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Scope:           p.ScopeLabel,
	}
	if p.Snapshot.Stale {
		prov.StaleWarning = "snapshot is stale"
	}

	payload := unifiPayload{
		Comment:         "skoed firewall-rule generator (see X-Skoed-Snapshot-* headers)",
		Name:            "skoed_doh_gap",
		Ruleset:         "WAN_OUT",
		Action:          action,
		SrcAddressGroup: src,
		DstAddressGroup: unifiAddrGroup{
			Type:    "address-group",
			Name:    "skoed_doh_resolvers",
			Members: members,
		},
		Provenance: prov,
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	return string(b) + "\n"
}
