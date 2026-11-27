package cluster

import (
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func openTestStore(t testing.TB) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newDynamicClient(t *testing.T, s *Store, ip string) (NewDynamicClientEntry, bool) {
	t.Helper()
	all, err := s.GetNewDynamicClients()
	if err != nil {
		t.Fatalf("get new dynamic clients: %v", err)
	}
	for _, e := range all {
		if e.ClientIP == ip {
			return e, true
		}
	}
	return NewDynamicClientEntry{}, false
}

// TestTrackNewDynamicClientKeepsFirstSeen checks that tracking an IP already
// in the list keeps its first FirstSeen (FS-NewDynamicClientAlertEndpoint).
func TestTrackNewDynamicClientKeepsFirstSeen(t *testing.T) {
	s := openTestStore(t)
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.TrackNewDynamicClient("192.0.2.10", first); err != nil {
		t.Fatal(err)
	}
	if err := s.TrackNewDynamicClient("192.0.2.10", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, ok := newDynamicClient(t, s, "192.0.2.10")
	if !ok {
		t.Fatal("192.0.2.10 not tracked")
	}
	if !e.FirstSeen.Equal(first) {
		t.Fatalf("FirstSeen = %v, want %v", e.FirstSeen, first)
	}
}

// TestTrackNewDynamicClientDoesNotResurrectDismissed checks that tracking a
// dismissed IP leaves it dismissed (FS-NewDynamicClientAlertDismissed).
func TestTrackNewDynamicClientDoesNotResurrectDismissed(t *testing.T) {
	s := openTestStore(t)
	if err := s.TrackNewDynamicClient("192.0.2.11", time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(CmdNewDynamicClientDismiss, NewDynamicClientDismissPayload{
		ClientIP: "192.0.2.11", DismissedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(raw); err != nil {
		t.Fatal(err)
	}
	if err := s.TrackNewDynamicClient("192.0.2.11", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := newDynamicClient(t, s, "192.0.2.11"); ok {
		t.Fatal("dismissed 192.0.2.11 is listed again after tracking")
	}
}

// TestTrackNewDynamicClientRecordsRemovedEntry checks that an IP whose entry
// disappeared from the bucket (e.g. after a snapshot restore) is recorded
// again by the next tracking call.
func TestTrackNewDynamicClientRecordsRemovedEntry(t *testing.T) {
	s := openTestStore(t)
	if err := s.TrackNewDynamicClient("192.0.2.12", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNewDynamicClients).Delete([]byte("192.0.2.12"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.TrackNewDynamicClient("192.0.2.12", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := newDynamicClient(t, s, "192.0.2.12"); !ok {
		t.Fatal("192.0.2.12 not recorded again after its entry was removed")
	}
}

// BenchmarkTrackNewDynamicClientKnown measures tracking an IP that is already
// recorded, the common case under steady traffic.
func BenchmarkTrackNewDynamicClientKnown(b *testing.B) {
	s := openTestStore(b)
	if err := s.TrackNewDynamicClient("192.0.2.13", time.Now()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := s.TrackNewDynamicClient("192.0.2.13", time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}
