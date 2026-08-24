package service_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alkaid/jubensha-carpool/backend/internal/config"
	"github.com/alkaid/jubensha-carpool/backend/internal/model"
	"github.com/alkaid/jubensha-carpool/backend/internal/service"
)

const commitFailureDriverName = "jubensha_commit_failure"

var errCommitRejected = errors.New("database rejected commit")

func init() {
	sql.Register(commitFailureDriverName, commitFailureDriver{})
}

func TestChatSendDoesNotPublishWhenCommitFails(t *testing.T) {
	db, err := sql.Open(commitFailureDriverName, "")
	if err != nil {
		t.Fatalf("open scripted database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	deps := service.NewDeps(&config.Config{MessageMaxLen: 500}, db, nil)
	pub := &recordingPublisher{}
	deps.SetPublisher(pub)
	chat := service.NewChatService(deps)

	msg, err := chat.Send(context.Background(), 42, 7, model.ChatSendData{
		MsgType:     model.MsgText,
		Content:     "commit failure must not look delivered",
		ClientMsgID: "client-commit-1",
	})

	if !errors.Is(err, errCommitRejected) {
		t.Errorf("Send error = %v, want commit error %v", err, errCommitRejected)
	}
	if msg != nil {
		t.Errorf("Send result = %#v, want nil when commit fails", msg)
	}
	if got := pub.roomPublishes.Load(); got != 0 {
		t.Errorf("room publishes = %d, want 0 before a successful commit", got)
	}
}

type recordingPublisher struct {
	roomPublishes atomic.Int64
}

func (p *recordingPublisher) PublishRoom(context.Context, int64, model.Envelope) {
	p.roomPublishes.Add(1)
}

func (*recordingPublisher) PublishWall(context.Context, model.Envelope) {}

func (*recordingPublisher) OnlineUserIDs(context.Context, int64) []int64 {
	return []int64{}
}

type commitFailureDriver struct{}

func (commitFailureDriver) Open(string) (driver.Conn, error) {
	return &commitFailureConn{}, nil
}

type commitFailureConn struct{}

func (*commitFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("scripted database does not prepare statements")
}

func (*commitFailureConn) Close() error { return nil }

func (*commitFailureConn) Begin() (driver.Tx, error) {
	return commitFailureTx{}, nil
}

func (*commitFailureConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return commitFailureTx{}, nil
}

func (*commitFailureConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (*commitFailureConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT EXISTS") && strings.Contains(query, "FROM room_members"):
		return rows([]string{"exists"}, []driver.Value{true}), nil
	case strings.Contains(query, "FROM room_messages m") && strings.Contains(query, "m.client_msg_id"):
		return emptyRows("id", "room_id", "seq", "sender_id", "msg_type", "content",
			"tag_code", "client_msg_id", "created_at", "sender_name", "sender_avatar"), nil
	case strings.Contains(query, "UPDATE rooms SET msg_seq = msg_seq + 1"):
		return rows([]string{"msg_seq"}, []driver.Value{int64(1)}), nil
	case strings.Contains(query, "INSERT INTO room_messages"):
		return rows([]string{"id", "created_at"}, []driver.Value{
			int64(101), time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
		}), nil
	case strings.Contains(query, "FROM users WHERE id = $1"):
		return emptyRows("id", "username", "phone", "password_hash", "nickname", "avatar",
			"city", "bio", "reputation", "created_at", "updated_at"), nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", strings.Join(strings.Fields(query), " "))
	}
}

type commitFailureTx struct{}

func (commitFailureTx) Commit() error   { return errCommitRejected }
func (commitFailureTx) Rollback() error { return nil }

type staticRows struct {
	columns []string
	values  [][]driver.Value
	next    int
}

func rows(columns []string, values ...[]driver.Value) driver.Rows {
	return &staticRows{columns: columns, values: values}
}

func emptyRows(columns ...string) driver.Rows {
	return &staticRows{columns: columns}
}

func (r *staticRows) Columns() []string { return r.columns }
func (*staticRows) Close() error        { return nil }

func (r *staticRows) Next(dest []driver.Value) error {
	if r.next >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.next])
	r.next++
	return nil
}
