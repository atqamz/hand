package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	legacyV18CutoverGateTimeout = 5 * time.Second
	legacyV18CutoverBarrierPoll = 10 * time.Millisecond
)

type legacyV18CutoverGate struct {
	info             SchemaInfo
	sourceSHA256     string
	db               *sql.DB
	conn             *sql.Conn
	releaseMigration func()
}

type sqliteConnQueryer struct {
	ctx  context.Context
	conn *sql.Conn
}

func (q sqliteConnQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	return q.conn.QueryContext(q.ctx, query, args...)
}

func (q sqliteConnQueryer) QueryRow(query string, args ...any) *sql.Row {
	return q.conn.QueryRowContext(q.ctx, query, args...)
}

func acquireLegacyV18CutoverGate(ctx context.Context, homeDir string) (*legacyV18CutoverGate, error) {
	return acquireLegacyV18CutoverGateWithTimeout(ctx, homeDir, legacyV18CutoverGateTimeout)
}

func acquireLegacyV18CutoverGateWithTimeout(parent context.Context, homeDir string, timeout time.Duration) (*legacyV18CutoverGate, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("acquire legacy v18 cutover gate: timeout must be positive")
	}

	releaseMigration, err := Lock(homeDir, MigrationLock, true)
	if err != nil {
		return nil, fmt.Errorf("acquire legacy v18 cutover MigrationLock: %w", err)
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			releaseMigration()
		}
	}()

	handoffCtx, handoffCancel := context.WithTimeout(parent, timeout)
	defer handoffCancel()
	path := Path(homeDir)

	readDB, err := openLegacyV18CutoverSQLite(path, "ro", timeout, true)
	if err != nil {
		return nil, err
	}
	readTx, err := readDB.BeginTx(handoffCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		_ = readDB.Close()
		return nil, fmt.Errorf("begin legacy v18 cutover SHARED snapshot: %w", err)
	}
	readOpen := true
	defer func() {
		if readOpen {
			_ = readTx.Rollback()
			_ = readDB.Close()
		}
	}()

	candidateInfo, err := validateLegacyV18CutoverSource(readTx)
	if err != nil {
		return nil, fmt.Errorf("validate legacy v18 cutover SHARED snapshot: %w", err)
	}
	candidateDigest, err := legacyV18CutoverFileSHA256(path)
	if err != nil {
		return nil, fmt.Errorf("hash legacy v18 cutover SHARED source: %w", err)
	}

	gateDB, err := openLegacyV18CutoverSQLite(path, "rw", timeout, false)
	if err != nil {
		return nil, err
	}
	gateConn, err := gateDB.Conn(handoffCtx)
	if err != nil {
		_ = gateDB.Close()
		return nil, fmt.Errorf("pin legacy v18 cutover EXCLUSIVE connection: %w", err)
	}
	gateOpen := true
	defer func() {
		if gateOpen {
			closeLegacyV18CutoverExclusive(gateConn, gateDB)
		}
	}()

	exclusive := make(chan error, 1)
	go func() {
		_, beginErr := gateConn.ExecContext(handoffCtx, `BEGIN EXCLUSIVE`)
		exclusive <- beginErr
	}()

	exclusiveDone, err := waitForLegacyV18CutoverReaderBarrier(handoffCtx, path, exclusive)
	if err != nil {
		if !exclusiveDone {
			handoffCancel()
			<-exclusive
		}
		return nil, err
	}

	barrierDigest, err := legacyV18CutoverFileSHA256(path)
	if err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("hash legacy v18 source under reader barrier: %w", err)
	}
	if barrierDigest != candidateDigest {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("legacy v18 cutover source changed under SHARED reader barrier: candidate=%s barrier=%s", candidateDigest, barrierDigest)
	}

	if err := readTx.Rollback(); err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("release legacy v18 cutover SHARED snapshot: %w", err)
	}
	if err := readDB.Close(); err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("close legacy v18 cutover SHARED source: %w", err)
	}
	readOpen = false

	select {
	case err := <-exclusive:
		if err != nil {
			return nil, fmt.Errorf("acquire legacy v18 cutover EXCLUSIVE gate: %w", err)
		}
	case <-handoffCtx.Done():
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("acquire legacy v18 cutover EXCLUSIVE gate: %w", handoffCtx.Err())
	}

	validationCtx, validationCancel := context.WithTimeout(parent, timeout)
	defer validationCancel()
	if _, err := gateConn.ExecContext(validationCtx, `PRAGMA query_only = 1`); err != nil {
		return nil, fmt.Errorf("set legacy v18 cutover EXCLUSIVE query_only: %w", err)
	}
	postInfo, err := validateLegacyV18CutoverSource(sqliteConnQueryer{ctx: validationCtx, conn: gateConn})
	if err != nil {
		return nil, fmt.Errorf("revalidate legacy v18 source under EXCLUSIVE gate: %w", err)
	}
	postDigest, err := legacyV18CutoverFileSHA256(path)
	if err != nil {
		return nil, fmt.Errorf("hash legacy v18 source under EXCLUSIVE gate: %w", err)
	}
	if postInfo != candidateInfo {
		return nil, fmt.Errorf("legacy v18 cutover source semantic identity changed across EXCLUSIVE handoff: candidate=%+v post=%+v", candidateInfo, postInfo)
	}
	if postDigest != candidateDigest {
		return nil, fmt.Errorf("legacy v18 cutover source changed across EXCLUSIVE handoff: candidate=%s post=%s", candidateDigest, postDigest)
	}

	gateOpen = false
	releaseOnError = false
	return &legacyV18CutoverGate{
		info:             postInfo,
		sourceSHA256:     postDigest,
		db:               gateDB,
		conn:             gateConn,
		releaseMigration: releaseMigration,
	}, nil
}

func waitForLegacyV18CutoverReaderBarrier(ctx context.Context, path string, exclusive <-chan error) (bool, error) {
	probeDB, err := openLegacyV18CutoverSQLite(path, "ro", 0, true)
	if err != nil {
		return false, err
	}
	defer func() { _ = probeDB.Close() }()

	ticker := time.NewTicker(legacyV18CutoverBarrierPoll)
	defer ticker.Stop()
	for {
		var objects int
		err := probeDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects)
		if isSQLiteBusy(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("probe legacy v18 cutover reader barrier: %w", err)
		}

		select {
		case beginErr := <-exclusive:
			if beginErr == nil {
				return true, fmt.Errorf("legacy v18 BEGIN EXCLUSIVE completed before known SHARED reader was released")
			}
			return true, fmt.Errorf("request legacy v18 cutover EXCLUSIVE gate: %w", beginErr)
		case <-ctx.Done():
			return false, fmt.Errorf("observe legacy v18 cutover reader barrier: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func openLegacyV18CutoverSQLite(path, mode string, busyTimeout time.Duration, queryOnly bool) (*sql.DB, error) {
	if mode != "ro" && mode != "rw" {
		return nil, fmt.Errorf("open legacy v18 cutover sqlite: unsupported mode %q", mode)
	}
	busyMilliseconds := int64(busyTimeout / time.Millisecond)
	if busyTimeout > 0 && busyMilliseconds == 0 {
		busyMilliseconds = 1
	}
	uri := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?mode=" + mode +
		"&_pragma=busy_timeout(" + strconv.FormatInt(busyMilliseconds, 10) + ")" +
		"&_pragma=foreign_keys(1)"
	if queryOnly {
		uri += "&_pragma=query_only(1)"
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open legacy v18 cutover sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func legacyV18CutoverFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func closeLegacyV18CutoverExclusive(conn *sql.Conn, db *sql.DB) {
	if conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), legacyV18CutoverGateTimeout)
		_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		cancel()
		_ = conn.Close()
	}
	if db != nil {
		_ = db.Close()
	}
}

func (g *legacyV18CutoverGate) Close() error {
	if g == nil {
		return nil
	}

	var firstErr error
	if g.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), legacyV18CutoverGateTimeout)
		if _, err := g.conn.ExecContext(ctx, `ROLLBACK`); err != nil {
			firstErr = fmt.Errorf("release legacy v18 cutover EXCLUSIVE gate: %w", err)
		}
		cancel()
		if err := g.conn.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close legacy v18 cutover EXCLUSIVE connection: %w", err)
		}
		g.conn = nil
	}
	if g.db != nil {
		if err := g.db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close legacy v18 cutover EXCLUSIVE database: %w", err)
		}
		g.db = nil
	}
	if g.releaseMigration != nil {
		g.releaseMigration()
		g.releaseMigration = nil
	}
	return firstErr
}
