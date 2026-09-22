package detaildb

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/jackc/pgx/v5/pgconn"
)

func sampleDB() dbconfig.DatabaseConfig {
	return dbconfig.DatabaseConfig{Host: "db.example.com", Port: 5432, DBName: "detail", User: "u", Password: "p"}
}

// testResolver resolves any host to 127.0.0.1 so the lookup reaches the fake
// connector without a real DNS lookup.
type testResolver struct{}

func (testResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

// fakeRows serves a fixed slice of rows; a nil cell produces a scan error to
// emulate a NULL scanned into a non-nullable int.
type fakeRows struct {
	rows [][]any
	idx  int
}

func (r *fakeRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return errors.New("scan past end")
	}
	row := r.rows[r.idx-1]
	for i := range dest {
		if row[i] == nil {
			return errors.New("NULL into non-nullable int")
		}
		switch d := dest[i].(type) {
		case *int:
			if v, ok := row[i].(int); ok {
				*d = v
			} else {
				return errors.New("non-integer cell")
			}
		default:
			return errors.New("unsupported scan dest")
		}
	}
	return nil
}

func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Err() error   { return nil }

type fakeConn struct {
	execErr error
	queryFn func() (Rows, error)
	closes  int
}

func (c *fakeConn) Exec(context.Context, string, ...any) error { return c.execErr }
func (c *fakeConn) Query(context.Context, string, ...any) (Rows, error) {
	return c.queryFn()
}
func (c *fakeConn) Close(context.Context) error { c.closes++; return nil }

// scriptedConnector returns scripted connect outcomes in order, recording the
// number of connect attempts.
type scriptedConnector struct {
	mu       sync.Mutex
	connects []func(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error)
	calls    int
}

func (c *scriptedConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (Conn, error) {
	c.mu.Lock()
	c.calls++
	idx := c.calls - 1
	c.mu.Unlock()
	if idx >= len(c.connects) {
		return nil, errors.New("scriptedConnector exhausted")
	}
	return c.connects[idx](ctx, host, port, user, password, dbname)
}

func connWith(query func() (Rows, error)) func(context.Context, string, int32, string, string, string) (Conn, error) {
	return func(context.Context, string, int32, string, string, string) (Conn, error) {
		return &fakeConn{queryFn: query}, nil
	}
}

func connErr(err error) func(context.Context, string, int32, string, string, string) (Conn, error) {
	return func(context.Context, string, int32, string, string, string) (Conn, error) {
		return nil, err
	}
}

func pgErr(code string) error { return &pgconn.PgError{Code: code} }

func setShortDelay(t *testing.T) {
	t.Helper()
	orig := transientRetryDelay
	transientRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { transientRetryDelay = orig })
}

func TestLookupSuccessOneRow(t *testing.T) {
	rows := &fakeRows{rows: [][]any{{2, 4, 100, 1001}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return rows, nil }),
	}}
	res, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err != nil {
		t.Fatalf("Lookup err = %v", err)
	}
	want := LookupResult{ArrivalRate: 2, ServiceRate: 4, RunDuration: 100, SeedPolicy: 1001}
	if res != want {
		t.Fatalf("Lookup = %+v, want %+v", res, want)
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1", c.calls)
	}
}

func TestLookupNoRowPermanent(t *testing.T) {
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return &fakeRows{rows: nil}, nil }),
	}}
	_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("no row must fail")
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1 (no retry)", c.calls)
	}
}

func TestLookupMultipleRowsPermanent(t *testing.T) {
	rows := &fakeRows{rows: [][]any{{2, 4, 100, 1001}, {3, 5, 120, 1002}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return rows, nil }),
	}}
	_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("multiple rows must fail")
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1 (no retry)", c.calls)
	}
}

func TestLookupNULLPermanent(t *testing.T) {
	rows := &fakeRows{rows: [][]any{{2, nil, 100, 1001}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return rows, nil }),
	}}
	_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("NULL must fail")
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1 (no retry)", c.calls)
	}
}

func TestLookupConstraintViolationPermanent(t *testing.T) {
	rows := &fakeRows{rows: [][]any{{0, 4, 100, 1001}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return rows, nil }),
	}}
	_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("constraint violation must fail")
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1 (no retry)", c.calls)
	}
}

func TestLookupTransientRetryThenSuccess(t *testing.T) {
	setShortDelay(t)
	good := &fakeRows{rows: [][]any{{2, 4, 100, 1001}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return nil, pgErr("08001") }),
		connWith(func() (Rows, error) { return good, nil }),
	}}
	res, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err != nil {
		t.Fatalf("Lookup err = %v", err)
	}
	if res.ArrivalRate != 2 {
		t.Fatalf("Lookup = %+v", res)
	}
	if c.calls != 2 {
		t.Fatalf("connects = %d, want 2 (one retry)", c.calls)
	}
}

func TestLookupTransientRetryThenTransientPermanent(t *testing.T) {
	setShortDelay(t)
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return nil, pgErr("53200") }),
		connWith(func() (Rows, error) { return nil, pgErr("57P03") }),
	}}
	_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("second transient must be permanent")
	}
	if c.calls != 2 {
		t.Fatalf("connects = %d, want 2", c.calls)
	}
}

func TestLookupPermanentNoRetry(t *testing.T) {
	for _, code := range []string{"28000", "42501", "42P01"} {
		c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
			connWith(func() (Rows, error) { return nil, pgErr(code) }),
		}}
		_, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
		if err == nil {
			t.Fatalf("SQLSTATE %s must fail", code)
		}
		if c.calls != 1 {
			t.Fatalf("SQLSTATE %s: connects = %d, want 1 (no retry)", code, c.calls)
		}
	}
}

func TestLookupConnectFailureRetries(t *testing.T) {
	setShortDelay(t)
	good := &fakeRows{rows: [][]any{{2, 4, 100, 1001}}}
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connErr(errors.New("connection refused")),
		connWith(func() (Rows, error) { return good, nil }),
	}}
	res, err := Lookup(context.Background(), sampleDB(), 1, testResolver{}, c)
	if err != nil {
		t.Fatalf("Lookup err = %v", err)
	}
	if res.ArrivalRate != 2 {
		t.Fatalf("Lookup = %+v", res)
	}
	if c.calls != 2 {
		t.Fatalf("connects = %d, want 2", c.calls)
	}
}

func TestLookupCancellationDuringDelayNoEmptyOutcome(t *testing.T) {
	transientRetryDelay = 5 * time.Second
	t.Cleanup(func() { transientRetryDelay = 30 * time.Second })
	ctx, cancel := context.WithCancel(context.Background())
	c := &scriptedConnector{connects: []func(context.Context, string, int32, string, string, string) (Conn, error){
		connWith(func() (Rows, error) { return nil, pgErr("08001") }),
	}}
	// Cancel during the delayed retry wait.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Lookup(ctx, sampleDB(), 1, testResolver{}, c)
	if err == nil {
		t.Fatal("cancelled lookup must fail")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled wrapper", err)
	}
	if c.calls != 1 {
		t.Fatalf("connects = %d, want 1 (no second attempt after cancel)", c.calls)
	}
}

func TestLookupUsesSchemaQualifiedSQL(t *testing.T) {
	if !contains(lookupSQL, "public.simulation_parameters") {
		t.Fatalf("lookup SQL %q must be schema-qualified", lookupSQL)
	}
	if !contains(lookupSQL, "WHERE parameterset_id = $1") {
		t.Fatalf("lookup SQL %q must parameterize on parameterset_id", lookupSQL)
	}
	if contains(lookupSQL, "search_path") {
		t.Fatalf("lookup SQL must not depend on search_path: %q", lookupSQL)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
