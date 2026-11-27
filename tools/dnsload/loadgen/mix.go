package loadgen

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// Kind is the kind of name a query asks for.
type Kind int

const (
	KindCached  Kind = iota // a name from the fixed set warmed before the run
	KindMiss                // a name never asked before, under the zone
	KindBlocked             // a name from the blocked-names list
)

// Query is the A query sent for one query number.
type Query struct {
	Name []byte // in wire format; nil makes the send fail
	Kind Kind
}

// Source gives the query for each query number of a run. Run calls it
// from several goroutines at once.
type Source interface {
	Query(n int64) Query
}

// SourceFunc makes a Source of a function giving the name, in text form,
// of each query. A name that cannot be encoded is counted as a send
// error.
func SourceFunc(f func(n int64) (name string, kind Kind)) Source {
	return sourceFunc(f)
}

type sourceFunc func(n int64) (string, Kind)

func (f sourceFunc) Query(n int64) Query {
	name, kind := f(n)
	wire, err := packName(name)
	if err != nil {
		return Query{Kind: kind}
	}
	return Query{Name: wire, Kind: kind}
}

// MixConfig describes the names of a run.
type MixConfig struct {
	Cached  []string // names warmed before the run
	Blocked []string // names the server blocks
	Zone    string   // zone of the never-asked names, e.g. perf.test
	Percent [3]int   // share of cached, miss and blocked queries; sums to 100
	RunID   string   // makes the never-asked names unique to the run
	Seed    uint64   // seeds the choice of names
}

// Mix is a Source drawing each name from the cached, never-asked and
// blocked names in the shares of its MixConfig. The name of query n
// depends only on the seed and n.
type Mix struct {
	cached   [][]byte // names in wire format
	blocked  [][]byte
	zone     []byte
	missFrom int // a pick of 0-99 at or above it is a miss
	blkFrom  int // a pick at or above it is a blocked name
	runID    string
	seed     uint64
}

// NewMix checks c and encodes its names.
func NewMix(c MixConfig) (*Mix, error) {
	p := c.Percent
	if p[0] < 0 || p[1] < 0 || p[2] < 0 || p[0]+p[1]+p[2] != 100 {
		return nil, fmt.Errorf("query mix %d/%d/%d: shares must be positive and sum to 100", p[0], p[1], p[2])
	}
	if p[0] > 0 && len(c.Cached) == 0 {
		return nil, fmt.Errorf("query mix: %d %% cached names but no cached name", p[0])
	}
	if p[2] > 0 && len(c.Blocked) == 0 {
		return nil, fmt.Errorf("query mix: %d %% blocked names but no blocked name", p[2])
	}
	if len(c.RunID) > 40 {
		return nil, fmt.Errorf("query mix: run ID %q longer than 40 characters", c.RunID)
	}
	m := &Mix{missFrom: p[0], blkFrom: p[0] + p[1], runID: c.RunID, seed: c.Seed}
	var err error
	if m.cached, err = packNames(c.Cached); err != nil {
		return nil, err
	}
	if m.blocked, err = packNames(c.Blocked); err != nil {
		return nil, err
	}
	if p[1] > 0 {
		if c.Zone == "" {
			return nil, fmt.Errorf("query mix: %d %% never-asked names but no zone", p[1])
		}
		if m.zone, err = packName(c.Zone); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Query returns the query of number n. Its name is shared between
// queries for cached and blocked names: do not change it.
func (m *Mix) Query(n int64) Query {
	// One draw: the low part picks the kind, the high part the name.
	x := rand.NewPCG(m.seed, uint64(n)).Uint64()
	pick, index := int(x%100), x>>32
	switch {
	case pick < m.missFrom:
		return Query{Name: m.cached[index%uint64(len(m.cached))], Kind: KindCached}
	case pick < m.blkFrom:
		return Query{Name: m.missName(n), Kind: KindMiss}
	default:
		return Query{Name: m.blocked[index%uint64(len(m.blocked))], Kind: KindBlocked}
	}
}

// missName is "u<run id>-<n in base 36>.<zone>": unique per query and
// per run, so it is never in the server's cache.
func (m *Mix) missName(n int64) []byte {
	label := "u" + m.runID + "-" + strconv.FormatInt(n, 36)
	wire := make([]byte, 0, 1+len(label)+len(m.zone))
	wire = append(wire, byte(len(label)))
	wire = append(wire, label...)
	return append(wire, m.zone...)
}

// GenerateCached returns count names c0.<zone> … c<count-1>.<zone>.
func GenerateCached(zone string, count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = "c" + strconv.Itoa(i) + "." + zone
	}
	return names
}

// ReadNames reads one name per line, skipping blank lines and lines
// starting with #. On a line with several fields, as in a hosts file,
// the last field is the name.
func ReadNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		names = append(names, fields[len(fields)-1])
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return names, nil
}

func packNames(names []string) ([][]byte, error) {
	out := make([][]byte, len(names))
	for i, name := range names {
		w, err := packName(name)
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

func packName(name string) ([]byte, error) {
	buf := make([]byte, 256)
	n, err := dns.PackDomainName(dns.Fqdn(name), buf, 0, nil, false)
	if err != nil {
		return nil, fmt.Errorf("name %q: %w", name, err)
	}
	return buf[:n], nil
}

// AppendQuery appends to buf a query with ID id, RD set, for the A
// record of qname (in wire format).
func AppendQuery(buf []byte, id uint16, qname []byte) []byte {
	buf = binary.BigEndian.AppendUint16(buf, id)
	buf = append(buf, 0x01, 0x00) // QR 0, opcode QUERY, RD 1
	buf = append(buf, 0, 1, 0, 0, 0, 0, 0, 0)
	buf = append(buf, qname...)
	buf = binary.BigEndian.AppendUint16(buf, dns.TypeA)
	return binary.BigEndian.AppendUint16(buf, dns.ClassINET)
}
