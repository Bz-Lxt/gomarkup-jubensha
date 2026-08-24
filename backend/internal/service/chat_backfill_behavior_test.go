package service_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

type backfillConnector struct{}

func (backfillConnector) Connect(context.Context) (driver.Conn, error) {
	return &backfillConn{}, nil
}

func (backfillConnector) Driver() driver.Driver { return backfillDriver{} }

type backfillDriver struct{}

func (backfillDriver) Open(string) (driver.Conn, error) { return &backfillConn{}, nil }

type backfillConn struct{}

func (*backfillConn) Prepare(query string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected Prepare: %s", query)
}

func (*backfillConn) Close() error { return nil }

func (*backfillConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("unexpected transaction")
}

func (*backfillConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (*backfillConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT EXISTS"):
		return &backfillRows{
			columns: []string{"exists"},
			values:  [][]driver.Value{{true}},
		}, nil
	case strings.Contains(query, "SELECT COALESCE(max(seq), 0)"):
		return &backfillRows{
			columns: []string{"latest_seq"},
			values:  [][]driver.Value{{int64(10)}},
		}, nil
	case strings.Contains(query, "ORDER BY m.seq DESC"):
		return newBackfillMessageRows(10, 9, 8), nil
	case strings.Contains(query, "ORDER BY m.seq ASC"):
		return newBackfillMessageRows(1, 2, 3), nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", strings.Join(strings.Fields(query), " "))
	}
}

type backfillRows struct {
	columns []string
	values  [][]driver.Value
	next    int
}

func (r *backfillRows) Columns() []string { return r.columns }
func (r *backfillRows) Close() error      { return nil }

func (r *backfillRows) Next(dest []driver.Value) error {
	if r.next >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.next])
	r.next++
	return nil
}

func newBackfillMessageRows(seqs ...int64) driver.Rows {
	createdAt := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	values := make([][]driver.Value, 0, len(seqs))
	for _, seq := range seqs {
		values = append(values, []driver.Value{
			seq, int64(42), seq, int64(7), "TEXT", "message", "", "", createdAt, "alice", "",
		})
	}
	return &backfillRows{
		columns: []string{
			"id", "room_id", "seq", "sender_id", "msg_type", "content", "tag_code",
			"client_msg_id", "created_at", "sender_name", "sender_avatar",
		},
		values: values,
	}
}

func TestChatService_BackfillLargeGapReturnsLatestWindow(t *testing.T) {
	pool := sql.OpenDB(backfillConnector{})
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })

	deps := service.NewDeps(&config.Config{BackfillMax: 3}, pool, nil)
	chat := service.NewChatService(deps)

	got, err := chat.Backfill(context.Background(), 42, 7, 0)
	if err != nil {
		t.Fatalf("Backfill returned an unexpected error: %v", err)
	}
	if !got.Truncated {
		t.Fatal("a gap larger than the backfill limit must be marked truncated")
	}
	if got.FromSeq != 7 || got.ToSeq != 10 || got.LatestSeq != 10 {
		t.Fatalf("backfill window = (%d,%d], latest=%d; want (7,10], latest=10",
			got.FromSeq, got.ToSeq, got.LatestSeq)
	}
	if got.TotalGap != 10 {
		t.Fatalf("TotalGap = %d, want 10", got.TotalGap)
	}

	want := []int64{8, 9, 10}
	if len(got.Messages) != len(want) {
		t.Fatalf("returned %d messages, want %d", len(got.Messages), len(want))
	}
	for i, seq := range want {
		if got.Messages[i].Seq != seq {
			t.Fatalf("Messages[%d].Seq = %d, want %d; truncated backfill must keep the newest window",
				i, got.Messages[i].Seq, seq)
		}
	}
}
