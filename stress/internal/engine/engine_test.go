package engine

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&pgconn.PgError{Code: "40001"}, "40001"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "dial", Err: errors.New("refused")}, "conn"},
		{errors.New("anything else"), "conn"},
	}
	for _, c := range cases {
		if got := classify(c.err); got != c.want {
			t.Errorf("classify(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
