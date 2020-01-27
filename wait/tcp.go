package wait

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

var (
	addrPattern = regexp.MustCompile(
		"^(?P<schema>(?P<proto>[A-Za-z]+)://)?(?P<host>[^#]+)(#(?P<freq>.+))?",
	)
	protoPort = map[string]string{
		"amqp":       "5672",
		"amqps":      "5671",
		"http":       "80",
		"https":      "443",
		"imap":       "143",
		"mysql":      "3306",
		"ldap":       "389",
		"ldaps":      "636",
		"postgresql": "5432",
		"smtp":       "25",
	}
)

// TCPSpec represents the input specification of a single TCP wait operation.
type TCPSpec struct {
	// Host is the hostname or IP address being waited.
	Host string
	// Port is the port number for the connection.
	Port string
	// PollFreq is how often a connection is attempted.
	PollFreq time.Duration
}

func (spec *TCPSpec) Addr() string {
	return net.JoinHostPort(spec.Host, spec.Port)
}

type Message interface {
	Status() Status
	Target() string
	Err() error
	ElapsedTime() time.Duration
}

type TCPMessage struct {
	spec      *TCPSpec
	status    Status
	startTime time.Time
	emitTime  time.Time
	err       error
}

func newTCPMessageStart(spec *TCPSpec, startTime time.Time) *TCPMessage {
	return &TCPMessage{
		spec:      spec,
		status:    Start,
		startTime: startTime,
		emitTime:  time.Now(),
		err:       nil,
	}
}

func newTCPMessageReady(spec *TCPSpec, startTime time.Time) *TCPMessage {
	return &TCPMessage{
		spec:      spec,
		status:    Ready,
		startTime: startTime,
		emitTime:  time.Now(),
		err:       nil,
	}
}

func newTCPMessageFailed(spec *TCPSpec, startTime time.Time, err error) *TCPMessage {
	return &TCPMessage{
		spec:      spec,
		status:    Failed,
		startTime: startTime,
		emitTime:  time.Now(),
		err:       err,
	}
}

func (msg *TCPMessage) Status() Status {
	return msg.status
}

// Addr is the address being waited.
func (msg *TCPMessage) Addr() string {
	if msg.spec == nil {
		return "<none>"
	}
	return msg.spec.Addr()
}

func (msg *TCPMessage) Target() string {
	if msg.spec == nil {
		return "<none>"
	}
	return "tcp://" + msg.Addr()
}

// ElapsedTime is the duration between waiting operation start and status emission.
func (msg *TCPMessage) ElapsedTime() time.Duration {
	return msg.emitTime.Sub(msg.startTime)
}

func (msg *TCPMessage) Err() error {
	return msg.err
}

type ctxKey int

const startTimeCtxKey ctxKey = 0

func newContext(bctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(bctx)
	return context.WithValue(ctx, startTimeCtxKey, time.Now()), cancel
}

func startTimeFromContext(ctx context.Context) time.Time {
	startTime, ok := ctx.Value(startTimeCtxKey).(time.Time)
	if !ok {
		return time.Now()
	}
	return startTime
}

func ParseTCPSpec(addr string, pollFreq time.Duration) (*TCPSpec, error) {
	var (
		proto             string
		rawHost           string
		hasPort, hasProto bool
		matches           = addrPattern.FindStringSubmatch(addr)
		subexpNames       = addrPattern.SubexpNames()
		groups            = make(map[string]string)
	)

	for i, value := range matches {
		groups[subexpNames[i]] = value
	}

	rawHost = groups["host"]
	hasPort = strings.ContainsRune(rawHost, ':')

	if hasPort {
		host, port, err := net.SplitHostPort(rawHost)
		if err != nil {
			return nil, err
		}
		groups["host"] = host
		groups["port"] = port
	} else if proto, hasProto = groups["proto"]; hasProto {
		if port, knownProto := protoPort[strings.ToLower(proto)]; knownProto {
			groups["host"] = rawHost
			groups["port"] = port
		} else {
			if proto == "" {
				return nil, fmt.Errorf("neither port nor protocol is given")
			}
			return nil, fmt.Errorf("port not given and protocol is unknown: %q", proto)
		}
	}

	if rawFreq, hasFreq := groups["freq"]; hasFreq && rawFreq != "" {
		freq, err := time.ParseDuration(rawFreq)
		if err != nil {
			return nil, err
		}
		pollFreq = freq
	}

	return &TCPSpec{
		Host:     groups["host"],
		Port:     groups["port"],
		PollFreq: pollFreq,
	}, nil
}

func ParseTCPSpecs(rawAddrs []string, defaultPollFreq time.Duration) ([]*TCPSpec, error) {
	specs := make([]*TCPSpec, len(rawAddrs))

	for i, rawAddr := range rawAddrs {
		spec, err := ParseTCPSpec(rawAddr, defaultPollFreq)
		if err != nil {
			return []*TCPSpec{}, err
		}
		specs[i] = spec
	}

	return specs, nil
}

// SingleTCP waits until a TCP connection can be made to the given address.
func SingleTCP(ctx context.Context, spec *TCPSpec) <-chan *TCPMessage {
	startTime := startTimeFromContext(ctx)
	out := make(chan *TCPMessage, 2)

	checkConn := func() *TCPMessage {
		_, err := net.DialTimeout("tcp", spec.Addr(), spec.PollFreq)

		if err == nil {
			return newTCPMessageReady(spec, startTime)
		}
		if shouldWait(err) {
			return nil
		}
		return newTCPMessageFailed(spec, startTime, err)
	}

	go func() {
		pollTicker := time.NewTicker(spec.PollFreq)
		defer pollTicker.Stop()

		defer close(out)

		out <- newTCPMessageStart(spec, startTime)

		// So that we start polling immediately, without waiting for the first tick.
		// There is no way to do this via the current ticker API.
		// See: https://github.com/golang/go/issues/17601
		if msg := checkConn(); msg != nil {
			out <- msg
			return
		}

		for {
			select {
			case <-ctx.Done():
				out <- newTCPMessageFailed(spec, startTime, ctx.Err())
				return

			case <-pollTicker.C:
				if msg := checkConn(); msg != nil {
					out <- msg
					return
				}
			}
		}
	}()

	return out
}

// AllTCP waits until connections can be made to all given TCP addresses.
func AllTCP(specs []*TCPSpec, waitTimeout time.Duration) <-chan Message {

	addrs := make([]string, len(specs))
	for i, spec := range specs {
		addrs[i] = spec.Addr()
	}

	var (
		chs         = make([](<-chan *TCPMessage), len(specs))
		out         = make(chan Message)
		ctx, cancel = newContext(context.Background())
	)

	for i, spec := range specs {
		chs[i] = SingleTCP(ctx, spec)
	}

	msgs := merge(chs)
	timeout := time.NewTimer(waitTimeout)

	go func() {
		defer timeout.Stop()
		defer cancel()
		defer close(out)

		for {
			select {
			case <-timeout.C:
				msg := newTCPMessageFailed(
					nil,
					startTimeFromContext(ctx),
					fmt.Errorf("exceeded timeout limit of %s", waitTimeout),
				)
				out <- msg
				return

			case msg, isOpen := <-msgs:
				if !isOpen {
					return
				}
				out <- msg
			}
		}
	}()

	return out
}
