package wait

import (
	"fmt"
	"testing"
	"time"
)

func TestParseTCPSpec(t *testing.T) {
	t.Parallel()

	var commonPollFreq = 1 * time.Second
	var commonStatusFreq = 5 * time.Second
	var tests = []struct {
		name    string
		in      string
		expSpec *TCPSpec
		expErr  error
	}{
		{
			"no protocol, no port",
			"localhost",
			nil,
			fmt.Errorf("neither port nor protocol is given"),
		},
		{
			"unknown protocol, no port",
			"foo://localhost",
			nil,
			fmt.Errorf("port not given and protocol is unknown: \"foo\""),
		},
		{
			"no protocol, port, no poll freq",
			"localhost:5000",
			&TCPSpec{
				Host:       "localhost",
				Port:       "5000",
				PollFreq:   commonPollFreq,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
		{
			"no protocol, port, poll freq",
			"localhost:5000#3s",
			&TCPSpec{
				Host:       "localhost",
				Port:       "5000",
				PollFreq:   3 * time.Second,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
		{
			"http, no port, no poll freq",
			"http://localhost",
			&TCPSpec{
				Host:       "localhost",
				Port:       "80",
				PollFreq:   commonPollFreq,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
		{
			"http, no port, poll freq",
			"http://localhost#500ms",
			&TCPSpec{
				Host:       "localhost",
				Port:       "80",
				PollFreq:   500 * time.Millisecond,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
		{
			"http, port, no poll freq",
			"http://localhost:3000",
			&TCPSpec{
				Host:       "localhost",
				Port:       "3000",
				PollFreq:   commonPollFreq,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
		{
			"http, port, poll freq",
			"http://localhost:3000#2s",
			&TCPSpec{
				Host:       "localhost",
				Port:       "3000",
				PollFreq:   2 * time.Second,
				StatusFreq: commonStatusFreq,
			},
			nil,
		},
	}

	for i, test := range tests {
		i := i
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			name := test.name
			expSpec := test.expSpec
			expErr := test.expErr

			obs, err := ParseTCPSpec(test.in, commonPollFreq, commonStatusFreq)

			if expErr != nil && err.Error() != expErr.Error() {
				t.Errorf("test[%d] %q failed - got error: %q, want: %q", i, name, err, expErr)
			}

			if expErr == nil && *expSpec != *obs {
				t.Errorf("test[%d] %q failed - got spec: %+v, want: %+v", i, name, *obs, *expSpec)
			}
		})
	}
}
