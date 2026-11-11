package linear

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// flaky fails the first request the way a dropped connection does.
type flaky struct{ calls int }

func (f *flaky) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	if f.calls == 1 {
		return nil, errors.New("write: socket is not connected")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"me"}}}`))}, nil
}

func TestReadRetriedOnceAfterNetworkError(t *testing.T) {
	f := &flaky{}
	c := &Client{key: "k", http: &http.Client{Transport: f}}
	var out struct{ Viewer struct{ ID string } }
	if err := c.do(context.Background(), `query { viewer { id } }`, nil, &out); err != nil || out.Viewer.ID != "me" {
		t.Fatalf("err %v, out %+v", err, out)
	}
	if f.calls != 2 {
		t.Fatalf("calls %d, want 2", f.calls)
	}
}

func TestMutationNotRetried(t *testing.T) {
	f := &flaky{}
	c := &Client{key: "k", http: &http.Client{Transport: f}}
	err := c.do(context.Background(), `mutation { x }`, nil, nil)
	if err == nil || f.calls != 1 {
		t.Fatalf("err %v, calls %d", err, f.calls)
	}
	if strings.Contains(err.Error(), "Post ") || !strings.Contains(err.Error(), "socket is not connected") {
		t.Fatalf("message: %v", err)
	}
}
