package service_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	carlock "github.com/alkaid/jubensha-carpool/backend/internal/lock"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

const joinRetryDriverName = "jubensha-join-retry"

func init() {
	sql.Register(joinRetryDriverName, joinRetryDriver{})
}

type joinRetryDriver struct{}

func (joinRetryDriver) Open(string) (driver.Conn, error) {
	return &joinRetryConn{}, nil
}

type joinRetryConn struct{}

func (c *joinRetryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("join retry fixture does not prepare statements")
}

func (c *joinRetryConn) Close() error { return nil }

func (c *joinRetryConn) Begin() (driver.Tx, error) { return joinRetryTx{}, nil }

func (c *joinRetryConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return joinRetryTx{}, nil
}

func (c *joinRetryConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *joinRetryConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "FROM rooms"):
		return lockedRoomRows(), nil
	case strings.Contains(query, "FROM room_members"):
		return existingMemberRows(), nil
	default:
		return nil, errors.New("join retry fixture received an unexpected query")
	}
}

type joinRetryTx struct{}

func (joinRetryTx) Commit() error   { return nil }
func (joinRetryTx) Rollback() error { return nil }

type joinRetryRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *joinRetryRows) Columns() []string { return r.columns }
func (r *joinRetryRows) Close() error      { return nil }

func (r *joinRetryRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func lockedRoomRows() driver.Rows {
	now := time.Now()
	return &joinRetryRows{
		columns: []string{
			"id", "owner_id", "title", "script_name", "venue_name", "city", "address", "room_type",
			"difficulty", "theme", "notes", "start_at", "capacity", "min_viable", "joined_count", "pending_count",
			"male_seats", "female_seats", "any_seats", "male_taken", "female_taken", "any_taken",
			"status", "msg_seq", "created_at", "updated_at",
		},
		values: [][]driver.Value{{
			int64(42), int64(7), "周末推理局", "长夜将尽", "城南店", "上海", "", string(model.TypeScript),
			int64(3), "硬核推理", "", now.Add(2 * time.Hour), int64(2), int64(2), int64(2), int64(0),
			int64(0), int64(0), int64(2), int64(0), int64(0), int64(2),
			string(model.RoomLocked), int64(4), now.Add(-time.Hour), now,
		}},
	}
}

func existingMemberRows() driver.Rows {
	now := time.Now()
	return &joinRetryRows{
		columns: []string{
			"id", "room_id", "user_id", "seat_gender", "status", "is_owner",
			"hold_expires_at", "joined_at", "left_at", "created_at", "updated_at",
		},
		values: [][]driver.Value{{
			int64(91), int64(42), int64(99), string(model.SeatAny), string(model.MemberJoined), false,
			nil, now.Add(-time.Minute), nil, now.Add(-time.Minute), now,
		}},
	}
}

func TestJoin_RetryAfterFillingLastSeatRemainsIdempotent(t *testing.T) {
	db, err := sql.Open(joinRetryDriverName, "")
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	guard := carlock.NewSlotGuard(
		carlock.NewCarSlotLocalLock(1),
		carlock.NewRedisLock(nil, time.Second, 0, false),
		time.Second,
	)
	deps := service.NewDeps(&config.Config{}, db, guard)
	slots := service.NewSlotService(deps)

	got, err := slots.Join(context.Background(), 42, 99, model.SeatAny)
	if err != nil {
		t.Fatalf("retrying a successful last-seat join returned an error: %v", err)
	}
	if got == nil {
		t.Fatal("retrying a successful join returned no result")
	}
	if !got.Idempotent {
		t.Fatal("retrying a successful join must be reported as idempotent")
	}
	if got.Member == nil || got.Member.ID != 91 || got.Member.UserID != 99 {
		t.Fatalf("retry returned the wrong existing membership: %+v", got.Member)
	}
	if got.Room == nil || got.Room.Status != model.RoomLocked {
		t.Fatalf("retry should preserve the room's locked state: %+v", got.Room)
	}
}
