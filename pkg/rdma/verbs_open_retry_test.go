//go:build linux && cgo && rdma

package rdma

import (
	"context"
	"fmt"
	"testing"
)

func TestIsRetriableOpenErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "canceled", err: context.Canceled, want: true},
		{
			name: "cm rejected",
			err:  fmt.Errorf("rdma verbs open: rdma_connect(established): unexpected cm event=RDMA_CM_EVENT_REJECTED expected=RDMA_CM_EVENT_ESTABLISHED status=8"),
			want: false,
		},
		{
			name: "generic open error",
			err:  fmt.Errorf("rdma verbs open: rdma_resolve_route: No such device"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetriableOpenErr(tc.err); got != tc.want {
				t.Fatalf("isRetriableOpenErr(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
