// Command dnsload sends DNS queries over UDP to one server at an even
// rate and reports, per second and per phase, the answers, the REFUSED
// responses, the queries lost and the latencies the functional spec of
// DNS overload protection checks (TS-DnsOverloadProtection § 9.2).
//
// With -serve it is instead a minimal UDP responder, to measure the
// load generator itself.
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"skoed/dnsload/loadgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dnsload:", err)
		os.Exit(1)
	}
}

type options struct {
	server      string
	qps         float64
	duration    time.Duration
	phases      string
	settle      time.Duration
	timeout     time.Duration
	workers     int
	zone        string
	cachedFile  string
	cachedCount int
	blockedFile string
	mix         string
	warm        bool
	csvPath     string
	seed        uint64
	serve       string
	refuseEvery int
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.server, "server", "", "DNS server, host:port")
	flag.Float64Var(&o.qps, "qps", 1000, "queries per second, for a run of one phase")
	flag.DurationVar(&o.duration, "duration", 10*time.Second, "length of a run of one phase")
	flag.StringVar(&o.phases, "phases", "", "phases rate:duration[:settle],... e.g. 40000:60s,5000:120s:5s (replaces -qps and -duration)")
	flag.DurationVar(&o.settle, "settle", time.Second, "start of each phase left out of its summary")
	flag.DurationVar(&o.timeout, "timeout", 5*time.Second, "a query with no response by then is lost")
	flag.IntVar(&o.workers, "workers", 0, "sockets (0: enough for the peak rate and the timeout)")
	flag.StringVar(&o.zone, "zone", "perf.test", "zone of the generated names")
	flag.StringVar(&o.cachedFile, "cached-file", "", "cached names, one per line (default: c0.<zone> … generated)")
	flag.IntVar(&o.cachedCount, "cached-count", 1000, "number of generated cached names")
	flag.StringVar(&o.blockedFile, "blocked-file", "", "blocked names, one per line or hosts format")
	flag.StringVar(&o.mix, "mix", "70,20,10", "percent of cached, never-asked and blocked names")
	flag.BoolVar(&o.warm, "warm", true, "query every cached name once before the run")
	flag.StringVar(&o.csvPath, "csv", "", "also write the per-second rows to this CSV file")
	flag.Uint64Var(&o.seed, "seed", 0, "seed of the name choice (0: random)")
	flag.StringVar(&o.serve, "serve", "", "run as a UDP responder on this address instead (self-test)")
	flag.IntVar(&o.refuseEvery, "refuse-every", 0, "with -serve, answer every Nth query REFUSED (0: never)")
	flag.Parse()
	return o
}

func run() error {
	o := parseFlags()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.serve != "" {
		return serve(ctx, o.serve, o.refuseEvery)
	}
	if o.server == "" {
		return fmt.Errorf("-server is required")
	}
	if o.seed == 0 {
		o.seed = randomUint64()
	}

	phases, err := buildPhases(o)
	if err != nil {
		return err
	}
	mix, cached, err := buildMix(o)
	if err != nil {
		return err
	}
	if o.warm && len(cached) > 0 {
		answered, err := loadgen.Warm(ctx, o.server, cached, o.timeout)
		if err != nil {
			return err
		}
		fmt.Printf("warm: %d of %d cached names answered\n", answered, len(cached))
	}

	var csvw *csv.Writer
	if o.csvPath != "" {
		f, err := os.Create(o.csvPath)
		if err != nil {
			return err
		}
		defer f.Close()
		csvw = csv.NewWriter(f)
		_ = csvw.Write(rowHeader)
		defer csvw.Flush()
	}

	fmt.Printf("server %s, timeout %v, %s\n", o.server, o.timeout, describePhases(phases))
	printRowHeader(os.Stdout)
	cpu0 := cpuTime()
	res, err := loadgen.Run(ctx, loadgen.Config{
		Server:  o.server,
		Phases:  phases,
		Timeout: o.timeout,
		Workers: o.workers,
		Mix:     mix,
		Seed:    o.seed,
	}, func(s loadgen.Second) {
		printRow(os.Stdout, &s)
		if csvw != nil {
			_ = csvw.Write(rowFields(&s))
		}
	})
	if err != nil {
		return err
	}
	printResult(os.Stdout, res, cpuTime()-cpu0)
	return nil
}

func buildPhases(o options) ([]loadgen.Phase, error) {
	if o.phases != "" {
		return loadgen.ParsePhases(o.phases, o.settle)
	}
	return loadgen.ParsePhases(fmt.Sprintf("%g:%s", o.qps, o.duration), o.settle)
}

func buildMix(o options) (*loadgen.Mix, []string, error) {
	var pct [3]int
	parts := strings.Split(o.mix, ",")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("-mix %q: want cached,never-asked,blocked", o.mix)
	}
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, nil, fmt.Errorf("-mix %q: %w", o.mix, err)
		}
		pct[i] = v
	}
	cached := loadgen.GenerateCached(o.zone, o.cachedCount)
	if o.cachedFile != "" {
		var err error
		if cached, err = loadgen.ReadNames(o.cachedFile); err != nil {
			return nil, nil, err
		}
	}
	if pct[0] == 0 {
		cached = nil
	}
	var blocked []string
	if o.blockedFile != "" {
		var err error
		if blocked, err = loadgen.ReadNames(o.blockedFile); err != nil {
			return nil, nil, err
		}
	}
	mix, err := loadgen.NewMix(loadgen.MixConfig{
		Cached:  cached,
		Blocked: blocked,
		Zone:    o.zone,
		Percent: pct,
		RunID:   strconv.FormatUint(o.seed, 36),
	})
	return mix, cached, err
}

func randomUint64() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:]) | 1
}

func describePhases(phases []loadgen.Phase) string {
	parts := make([]string, len(phases))
	for i, p := range phases {
		parts[i] = fmt.Sprintf("%.0f qps × %v", p.Rate, p.Duration)
	}
	return strings.Join(parts, ", then ")
}

var rowHeader = []string{"second", "sent", "answered", "refused", "other_rcode", "lost", "ans_p50_ms", "ans_p99_ms", "all_p999_ms"}

func rowFields(s *loadgen.Second) []string {
	return []string{
		strconv.Itoa(s.Index),
		strconv.FormatInt(s.Sent, 10),
		strconv.FormatInt(s.Answered(), 10),
		strconv.FormatInt(s.Refused(), 10),
		strconv.FormatInt(s.Other(), 10),
		strconv.FormatInt(s.Lost, 10),
		ms(s.Ans.Quantile(0.5)),
		ms(s.Ans.Quantile(0.99)),
		ms(s.All.Quantile(0.999)),
	}
}

func printRowHeader(w io.Writer) {
	fmt.Fprintf(w, "%6s %8s %8s %8s %6s %7s %9s %9s %10s\n",
		"second", "sent", "answered", "refused", "other", "lost", "ans p50", "ans p99", "all p99.9")
}

func printRow(w io.Writer, s *loadgen.Second) {
	f := rowFields(s)
	fmt.Fprintf(w, "%6s %8s %8s %8s %6s %7s %9s %9s %10s\n", f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8])
}

func printResult(w io.Writer, res *loadgen.Result, cpu time.Duration) {
	for i, p := range res.Phases {
		s := &p.Summary
		fmt.Fprintf(w, "\nphase %d: %.0f qps × %v, summary over seconds [%d, %d)\n", i+1, p.Phase.Rate, p.Phase.Duration, p.From, p.To)
		fmt.Fprintf(w, "  sent %d, answered %d, REFUSED %d, other RCODE %d (in answered), lost %d\n",
			s.Sent, s.Answered, s.Refused(), s.Other(), s.Lost)
		fmt.Fprintf(w, "  answered/s: mean %.1f, lowest minute %.1f, lowest second %d (second %d)\n",
			s.AnsweredPerSecond(), s.MinMinuteAnsweredPerSecond, s.MinSecondAnswered, s.MinSecond)
		fmt.Fprintf(w, "  answered or REFUSED %.3f %%, REFUSED %.3f %%, lost %.3f %%\n",
			s.PercentAnsweredOrRefused(), s.PercentRefused(), s.PercentLost())
		fmt.Fprintf(w, "  answers p50 %s ms, p99 %s ms; all responses p99.9 %s ms\n",
			ms(s.Ans.Quantile(0.5)), ms(s.Ans.Quantile(0.99)), ms(s.All.Quantile(0.999)))
	}
	// The elapsed time includes waiting for the timeout of the last
	// queries, so CPU per query is the figure to compare between rates.
	var sent int64
	for i := range res.Seconds {
		sent += res.Seconds[i].Sent
	}
	fmt.Fprintf(w, "\nload generator: %d workers, %.2f s elapsed, CPU %.2f s (%.2f cores, %.1f µs per query), send lag p99 %s ms, max %s ms\n",
		res.Workers, res.Elapsed.Seconds(), cpu.Seconds(), cpu.Seconds()/res.Elapsed.Seconds(),
		float64(cpu.Microseconds())/float64(max(sent, 1)),
		ms(res.SendLag.Quantile(0.99)), ms(res.SendLag.Quantile(1)))
	if res.Late+res.Evicted+res.SendErrors > 0 {
		fmt.Fprintf(w, "warning: %d late or unmatched responses, %d queries evicted before their timeout, %d send errors\n",
			res.Late, res.Evicted, res.SendErrors)
	}
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64)
}

// cpuTime is the user and system CPU time used by the process.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
