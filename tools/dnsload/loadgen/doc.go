// Package loadgen sends A queries over UDP to one DNS server at an even
// rate and records what each query received: an answer (NOERROR or
// NXDOMAIN), a REFUSED response, another response (e.g. SERVFAIL), no
// response within the timeout (lost), or a failed local send.
//
// It is the client code of tools/dnsload and is meant to be shared with
// the acceptance tests of DNS overload protection
// (TS-DnsOverloadProtection § 9.2). It does not print and does not
// exit; errors are returned.
//
// # Running a load
//
// A run is a list of phases, each a rate held for a duration:
//
//	res, err := loadgen.Run(ctx, loadgen.Config{
//		Server:  "10.0.0.12:53",
//		Phases:  []loadgen.Phase{{Rate: 4000, Duration: time.Minute, Settle: time.Second}},
//		Timeout: 5 * time.Second,
//		Source:  source,
//	}, nil)
//
// Query k of a phase with rate r is sent k/r after the phase starts, so a
// late send does not shift the following ones. The queries are shared
// round-robin between workers, each with its own UDP socket; a response
// is matched to its query by DNS ID and question name.
//
// Source gives the name of each query. Mix draws from cached,
// never-asked and blocked names in set shares (70/20/10 in the
// functional spec); SourceFunc wraps a function, so a test can supply
// its own names:
//
//	source := loadgen.SourceFunc(func(n int64) (string, loadgen.Kind) {
//		if n%10 == 9 {
//			return "tracker.example.com", loadgen.KindBlocked
//		}
//		return fmt.Sprintf("c%d.perf.test", n%1000), loadgen.KindCached
//	})
//
// Config.Start sets a start time in the future, and cancelling ctx stops
// the sending early; the queries already sent still get their outcome.
//
// # Results
//
// Result.Seconds has one Second per second of the run. A query counts in
// the second it was sent, whenever its response comes, and in exactly
// one of: Answered, Refused, Other, Lost, SendErrors. Each Second has two
// latency histograms: Ans for the answers and All for every response
// (answers, REFUSED and other).
//
// Result.Phases summarizes each phase twice: Summary over the whole
// phase, for the shares and the latencies, and Settled over the phase
// minus its settle time, for the answers per second.
//
// With Config.Records, Result.Queries also has one QueryRecord per query
// sent, with its kind, send time, latency, outcome, RCODE and the
// addresses of the A records of its response. Use it at test rates: it
// keeps every query in memory and parses every response in full.
//
// Summarize adds up any range of seconds, e.g. the window of a scenario
// that starts a few seconds into a phase.
package loadgen
