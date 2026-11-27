package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
)

// serve answers every UDP query on addr with an empty NOERROR response,
// or REFUSED for every refuseEvery-th query, until ctx is done. It does
// no DNS work, so that a load test against it measures the load
// generator.
func serve(ctx context.Context, addr string, refuseEvery int) error {
	laddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	fmt.Printf("serving on %s\n", conn.LocalAddr())
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	var count atomic.Uint64
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			buf := make([]byte, 65535)
			for {
				n, from, err := conn.ReadFromUDPAddrPort(buf)
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					continue
				}
				if n < 12 || buf[2]&0x80 != 0 {
					continue
				}
				rcode := byte(0)
				if refuseEvery > 0 && count.Add(1)%uint64(refuseEvery) == 0 {
					rcode = 5 // REFUSED
				}
				buf[2] |= 0x80        // QR
				buf[3] = 0x80 | rcode // RA, RCODE
				_, _ = conn.WriteToUDPAddrPort(buf[:n], from)
			}
		})
	}
	wg.Wait()
	return nil
}
