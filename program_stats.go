package main

import (
	"fmt"
	"github.com/bmizerany/perks/quantile"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
)

const (
	ALLTIME_MARKER = "alltime."
)

type NamedValue struct {
	value float64
	name  string
}

func NewNamedValue(name string, value float64) NamedValue {
	return NamedValue{name: name, value: value}
}

type ProgramStats struct {
	lost, drops      *Counter
	stats            map[string]*quantile.Stream
	input            <-chan NamedValue
	network, address string
	sync.Mutex
}

func NewProgramStats(on string, lost, drops *Counter, input <-chan NamedValue) *ProgramStats {
	var network, address string
	if len(on) == 0 {
		network = ""
		address = ""
	} else {
		netDeets := strings.Split(on, ",")
		switch len(netDeets) {
		case 2:
			network = netDeets[0]
			address = netDeets[1]
		default:
			log.Fatalf("Invalid -stats-addr (%s). Must be of form <net>,<addr> (e.g. unix,/tmp/ff)\n", on)
		}
	}

	return &ProgramStats{
		input:   input,
		lost:    lost,
		drops:   drops,
		network: network,
		address: address,
		stats:   make(map[string]*quantile.Stream),
	}
}

func updateSampleInMap(m map[string]*quantile.Stream, name string, value float64) {
	var sample *quantile.Stream

	sample, ok := m[name]
	if !ok {
		sample = quantile.NewTargeted(0.50, 0.95, 0.99)
	}

	sample.Insert(value)
	m[name] = sample
}

func (stats *ProgramStats) Run() {
	var listener net.Listener
	var err error

	if stats.network != "" {
		unixSocket := stats.network == "unix"

		if unixSocket {
			if Exists(stats.address) {
				err = os.Remove(stats.address)
				if err != nil {
					log.Fatalf("Unable to remove %s to setup stats socket: %s\n", stats.address, err)
				}
			}
		}

		listener, err = net.Listen(stats.network, stats.address)
		if err != nil {
			log.Fatalf("Unable to listen on %s,%s: %s\n", stats.network, stats.address, err)
		}

		go stats.accept(listener)
	}

	go func() {
		stats.aggregateValues()
		if listener != nil {
			listener.Close()
		}

		//FIXME: Chances are that we won't get here because we'll exit before this
		if stats.network == "unix" {
			if Exists(stats.address) {
				err := os.Remove(stats.address)
				if err != nil {
					log.Printf("Unable to remove socket (%s): %s\n", stats.address, err)
				}
			}
		}
	}()

}

func (stats *ProgramStats) aggregateValues() {
	for namedValue := range stats.input {
		stats.Mutex.Lock()
		updateSampleInMap(stats.stats, namedValue.name, namedValue.value)
		updateSampleInMap(stats.stats, ALLTIME_MARKER+namedValue.name, namedValue.value)
		stats.Mutex.Unlock()
	}
}

func (stats *ProgramStats) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %s\n", err)
			break
		}
		go stats.handleConnection(conn)
	}
}

func (stats *ProgramStats) handleConnection(conn net.Conn) {
	defer conn.Close()

	output := make([]string, 0, len(stats.stats)+2)
	output = append(output, fmt.Sprintf("log-shuttle.alltime.drops: %d\n", stats.drops.AllTime()))
	output = append(output, fmt.Sprintf("log-shuttle.alltime.lost: %d\n", stats.lost.AllTime()))

	stats.Mutex.Lock()
	for name, stream := range stats.stats {
		output = append(output, fmt.Sprintf("log-shuttle.%s.count: %d\n", name, stream.Count()))
		output = append(output, fmt.Sprintf("log-shuttle.%s.p50: %f\n", name, stream.Query(0.50)))
		output = append(output, fmt.Sprintf("log-shuttle.%s.p95: %f\n", name, stream.Query(0.95)))
		output = append(output, fmt.Sprintf("log-shuttle.%s.p99: %f\n", name, stream.Query(0.99)))
		if name[0:8] != ALLTIME_MARKER {
			stream.Reset()
		}
	}
	stats.Mutex.Unlock()

	sort.Strings(output)

	for i := range output {
		_, err := conn.Write([]byte(output[i]))
		if err != nil {
			log.Printf("Error writting stats out: %s\n", err)
		}
	}
}