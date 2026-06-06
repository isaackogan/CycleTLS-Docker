package main

import (
	"bytes"
	"net"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// startTime is captured at process start so we can expose an uptime gauge.
var startTime = time.Now()

var (
	connectedClients = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cycletls_connected_clients",
		Help: "Number of WebSocket clients currently connected.",
	})
	messagesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cycletls_messages_received_total",
		Help: "Total number of WebSocket data messages received from clients.",
	})
	messagesSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cycletls_messages_sent_total",
		Help: "Total number of WebSocket data messages sent to clients.",
	})
	bytesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cycletls_bytes_received_total",
		Help: "Total number of WebSocket payload+framing bytes received from clients.",
	})
	bytesSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cycletls_bytes_sent_total",
		Help: "Total number of WebSocket payload+framing bytes sent to clients.",
	})
)

// registerMetrics wires our custom collectors into the default Prometheus
// registry. The default registry already ships with the Go runtime collector
// (go_goroutines, go_memstats_*) and the process collector
// (process_cpu_seconds_total, process_resident_memory_bytes, ...), so CPU and
// memory are exported for free alongside the metrics below.
func registerMetrics() {
	prometheus.MustRegister(
		connectedClients,
		messagesReceived,
		messagesSent,
		bytesReceived,
		bytesSent,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "cycletls_uptime_seconds",
			Help: "Seconds since the process started.",
		}, func() float64 { return time.Since(startTime).Seconds() }),
	)
}

// meteredListener wraps a net.Listener so every accepted connection is metered.
type meteredListener struct {
	net.Listener
}

func (l meteredListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	return &meteredConn{Conn: c}, nil
}

// meteredConn counts WebSocket traffic on a single connection.
//
// Every connection begins life as a plain HTTP request (the health check, or
// the WebSocket upgrade handshake). We only start accounting for WebSocket
// metrics once we observe the server writing the "101 Switching Protocols"
// response, which guarantees the bytes that follow are WebSocket frames and
// keeps non-WebSocket traffic (e.g. /health_check) out of the numbers.
type meteredConn struct {
	net.Conn
	upgraded  bool
	inParser  frameParser
	outParser frameParser
	closeOnce sync.Once
}

// untrack decrements the connected-clients gauge exactly once, no matter how
// the connection ends. cycletls' writeSocket can block indefinitely after the
// read side dies (so WSEndpoint may never return), which is why we hang the
// gauge off the TCP lifecycle here instead of off the HTTP handler.
func (c *meteredConn) untrack() {
	c.closeOnce.Do(connectedClients.Dec)
}

func (c *meteredConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if !c.upgraded {
		// The handshake response itself is HTTP, not a frame, so flip the flag
		// and skip parsing this buffer. Everything written afterwards is frames.
		if bytes.HasPrefix(p, []byte("HTTP/1.1 101")) {
			c.upgraded = true
			connectedClients.Inc()
		}
		return n, err
	}
	if n > 0 {
		bytesSent.Add(float64(n))
		c.outParser.feed(p[:n], messagesSent)
	}
	return n, err
}

func (c *meteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.upgraded {
		if n > 0 {
			bytesReceived.Add(float64(n))
			c.inParser.feed(p[:n], messagesReceived)
		}
		// A read error on an upgraded conn means the client is gone.
		if err != nil {
			c.untrack()
		}
	}
	return n, err
}

func (c *meteredConn) Close() error {
	if c.upgraded {
		c.untrack()
	}
	return c.Conn.Close()
}

// frameParser is an incremental WebSocket frame scanner. It is fed raw bytes as
// they stream past and increments the supplied counter once for every new data
// (text/binary) message. Continuation and control frames (ping/pong/close) are
// parsed so the stream stays aligned but are not counted as messages.
type frameParser struct {
	state     int    // 0:opcode 1:len-byte 2:ext-len 3:mask-key 4:payload
	remaining uint64 // bytes left in the current section
	pending   int    // ext-len / mask-key bytes still to read
	extLen    uint64
	masked    bool
}

const (
	stOpcode = iota
	stLenByte
	stExtLen
	stMaskKey
	stPayload
)

func (fp *frameParser) feed(buf []byte, counter prometheus.Counter) {
	for i := 0; i < len(buf); i++ {
		b := buf[i]
		switch fp.state {
		case stOpcode:
			opcode := b & 0x0f
			if opcode == 0x1 || opcode == 0x2 { // text or binary => new message
				counter.Inc()
			}
			fp.state = stLenByte
		case stLenByte:
			fp.masked = b&0x80 != 0
			l := b & 0x7f
			switch {
			case l < 126:
				fp.remaining = uint64(l)
				fp.afterLen()
			case l == 126:
				fp.pending, fp.extLen = 2, 0
				fp.state = stExtLen
			default: // 127
				fp.pending, fp.extLen = 8, 0
				fp.state = stExtLen
			}
		case stExtLen:
			fp.extLen = fp.extLen<<8 | uint64(b)
			if fp.pending--; fp.pending == 0 {
				fp.remaining = fp.extLen
				fp.afterLen()
			}
		case stMaskKey:
			if fp.pending--; fp.pending == 0 {
				fp.toPayload()
			}
		case stPayload:
			avail := uint64(len(buf) - i)
			skip := fp.remaining
			if skip > avail {
				skip = avail
			}
			fp.remaining -= skip
			i += int(skip) - 1 // -1 because the loop will ++ again
			if fp.remaining == 0 {
				fp.state = stOpcode
			}
		}
	}
}

// afterLen advances past the length field, into the mask key (if the frame is
// masked, as client->server frames always are) and then the payload.
func (fp *frameParser) afterLen() {
	if fp.masked {
		fp.pending = 4
		fp.state = stMaskKey
		return
	}
	fp.toPayload()
}

func (fp *frameParser) toPayload() {
	if fp.remaining == 0 {
		fp.state = stOpcode
		return
	}
	fp.state = stPayload
}
