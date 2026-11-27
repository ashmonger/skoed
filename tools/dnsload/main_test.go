package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"

	"skoed/dnsload/loadgen"
)

func TestWriteCSV(t *testing.T) {
	seconds := make([]loadgen.Second, 2)
	for i := range seconds {
		s := &seconds[i]
		s.Index = i
		s.Sent = 10
		s.Rcodes[dns.RcodeSuccess] = 4
		s.Rcodes[dns.RcodeNameError] = 1
		s.Rcodes[dns.RcodeRefused] = 2
		s.Rcodes[dns.RcodeServerFailure] = 1
		s.Lost = 1
		s.SendErrors = 1
		for range 5 {
			s.Ans.Add(2 * time.Millisecond)
			s.All.Add(2 * time.Millisecond)
		}
		for range 3 {
			s.All.Add(time.Second)
		}
	}
	var buf bytes.Buffer
	if err := writeCSV(&buf, seconds); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want a header and 2", len(rows))
	}
	want := map[string]string{
		"second": "1", "sent": "10", "answered": "5", "refused": "2", "other_rcode": "1",
		"lost": "1", "send_errors": "1",
	}
	for col, name := range rows[0] {
		if v, ok := want[name]; ok && rows[2][col] != v {
			t.Errorf("column %s = %q, want %q", name, rows[2][col], v)
		}
	}
	// Percentiles are bucket upper bounds, at most 3 % above the value.
	for col, want := range map[int]float64{7: 2, 8: 2, 9: 1000} {
		got, err := strconv.ParseFloat(rows[2][col], 64)
		if err != nil || got < want || got > want*1.04 {
			t.Errorf("column %s = %q, want %v to %v", rows[0][col], rows[2][col], want, want*1.04)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteCSVReportsWriteErrors(t *testing.T) {
	if err := writeCSV(failingWriter{}, make([]loadgen.Second, 1)); err == nil {
		t.Error("want the write error")
	}
}
