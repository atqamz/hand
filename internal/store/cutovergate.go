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
	archiveCandidate legacyV18CutoverArchiveCandidate
	source           *legacyV18CutoverPinnedSource
	readDB           *sql.DB
	readConn         *sql.Conn
	probeDB          *sql.DB
	probeConn        *sql.Conn
	db               *sql.DB
	conn             *sql.Conn
	releaseMigration func()
}

type legacyV18CutoverPinnedSource struct {
	path     string
	file     *os.File
	info     os.FileInfo
	size     int64
	retained []*os.File
}

func openLegacyV18CutoverPinnedSource(path string) (*legacyV18CutoverPinnedSource, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy v18 cutover source: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("legacy v18 cutover source %s is not a direct regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open legacy v18 cutover source: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened legacy v18 cutover source: %w", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("opened legacy v18 cutover source %s is not a regular file", path)
	}
	if !os.SameFile(pathInfo, opened) {
		return nil, fmt.Errorf("legacy v18 cutover source %s changed identity while being opened", path)
	}
	if err := requireLegacyV18CutoverSingleSourceLink(file, opened); err != nil {
		return nil, err
	}
	source := &legacyV18CutoverPinnedSource{path: path, file: file, info: opened, size: opened.Size()}
	if err := source.revalidate(); err != nil {
		return nil, err
	}
	closeOnError = false
	return source, nil
}

func (s *legacyV18CutoverPinnedSource) revalidate() error {
	if s == nil || s.file == nil || s.info == nil {
		return fmt.Errorf("legacy v18 cutover pinned source is not live")
	}
	opened, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("restat pinned legacy v18 cutover source: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(s.info, opened) {
		return fmt.Errorf("pinned legacy v18 cutover source changed identity")
	}
	if opened.Size() != s.size {
		return fmt.Errorf("pinned legacy v18 cutover source size=%d, want %d", opened.Size(), s.size)
	}
	if err := requireLegacyV18CutoverSingleSourceLink(s.file, opened); err != nil {
		return err
	}
	current, err := os.Lstat(s.path)
	if err != nil {
		return fmt.Errorf("reinspect pinned legacy v18 cutover source: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return fmt.Errorf("legacy v18 cutover source %s changed away from a direct regular file", s.path)
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("legacy v18 cutover source %s changed identity while pinned", s.path)
	}
	if current.Size() != s.size {
		return fmt.Errorf("legacy v18 cutover source path size=%d, want %d", current.Size(), s.size)
	}
	return nil
}

func requireLegacyV18CutoverSingleSourceLink(file *os.File, info os.FileInfo) error {
	links, err := legacyV18CutoverSourceLinkCount(file, info)
	if err != nil {
		return fmt.Errorf("inspect legacy v18 cutover source link count: %w", err)
	}
	if links != 1 {
		return fmt.Errorf("legacy v18 cutover source has %d filesystem links, want exactly 1", links)
	}
	return nil
}

func (s *legacyV18CutoverPinnedSource) sha256() (string, error) {
	hash := sha256.New()
	if err := s.copyTo(hash); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (s *legacyV18CutoverPinnedSource) copyTo(dst io.Writer) error {
	if err := s.revalidate(); err != nil {
		return err
	}
	written, err := io.Copy(dst, io.NewSectionReader(s.file, 0, s.size))
	if err != nil {
		return err
	}
	if written != s.size {
		return fmt.Errorf("read pinned legacy v18 cutover source bytes=%d, want %d", written, s.size)
	}
	return s.revalidate()
}

func (s *legacyV18CutoverPinnedSource) openDistinctArtifact(path, role string, flag int) (*os.File, os.FileInfo, error) {
	if err := s.revalidate(); err != nil {
		return nil, nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect legacy v18 cutover %s: %w", role, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("legacy v18 cutover %s %s is not a direct regular file", role, path)
	}
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open legacy v18 cutover %s: %w", role, err)
	}
	opened, err := file.Stat()
	if err != nil {
		s.retained = append(s.retained, file)
		return nil, nil, fmt.Errorf("stat opened legacy v18 cutover %s: %w", role, err)
	}
	if os.SameFile(s.info, opened) {
		s.retained = append(s.retained, file)
		return nil, nil, fmt.Errorf("legacy v18 cutover %s resolves to the active source", role)
	}
	closeOnError := func(err error) (*os.File, os.FileInfo, error) {
		_ = file.Close()
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(pathInfo, opened) {
		return closeOnError(fmt.Errorf("legacy v18 cutover %s changed identity while being opened", role))
	}
	if err := s.revalidate(); err != nil {
		return closeOnError(err)
	}
	return file, opened, nil
}

func (s *legacyV18CutoverPinnedSource) validateDistinctArtifact(path, role string, file *os.File, expected os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("restat legacy v18 cutover %s: %w", role, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || opened.Size() != expected.Size() {
		return fmt.Errorf("legacy v18 cutover %s changed while open", role)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect legacy v18 cutover %s: %w", role, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != expected.Size() {
		return fmt.Errorf("legacy v18 cutover %s path changed while open", role)
	}
	return s.revalidate()
}

func (s *legacyV18CutoverPinnedSource) distinctArtifactSHA256(path, role string) (string, error) {
	file, info, err := s.openDistinctArtifact(path, role, os.O_RDONLY)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.NewSectionReader(file, 0, info.Size()))
	if copyErr == nil && written != info.Size() {
		copyErr = fmt.Errorf("read bytes=%d, want %d", written, info.Size())
	}
	if copyErr == nil {
		copyErr = s.validateDistinctArtifact(path, role, file, info)
	}
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", fmt.Errorf("close legacy v18 cutover %s: %w", role, closeErr)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (s *legacyV18CutoverPinnedSource) readDistinctArtifact(path, role string) ([]byte, error) {
	file, info, err := s.openDistinctArtifact(path, role, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(io.NewSectionReader(file, 0, info.Size()))
	if readErr == nil && int64(len(payload)) != info.Size() {
		readErr = fmt.Errorf("read bytes=%d, want %d", len(payload), info.Size())
	}
	if readErr == nil {
		readErr = s.validateDistinctArtifact(path, role, file, info)
	}
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close legacy v18 cutover %s: %w", role, closeErr)
	}
	return payload, nil
}

func (s *legacyV18CutoverPinnedSource) syncDistinctArtifact(path, role string) error {
	file, info, err := s.openDistinctArtifact(path, role, os.O_RDWR)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	if syncErr == nil {
		syncErr = s.validateDistinctArtifact(path, role, file, info)
	}
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return fmt.Errorf("close legacy v18 cutover %s: %w", role, closeErr)
	}
	return nil
}

func legacyV18CutoverArtifactSHA256(source *legacyV18CutoverPinnedSource, path, role string) (string, error) {
	if source == nil || source.file == nil {
		return legacyV18CutoverFileSHA256(path)
	}
	return source.distinctArtifactSHA256(path, role)
}

func readLegacyV18CutoverArtifact(source *legacyV18CutoverPinnedSource, path, role string) ([]byte, error) {
	if source == nil || source.file == nil {
		return os.ReadFile(path)
	}
	return source.readDistinctArtifact(path, role)
}

func syncLegacyV18CutoverArtifact(source *legacyV18CutoverPinnedSource, path, role string) error {
	if source == nil || source.file == nil {
		return syncLegacyV18CutoverFile(path)
	}
	return source.syncDistinctArtifact(path, role)
}

func (s *legacyV18CutoverPinnedSource) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	for _, file := range s.retained {
		if err := file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.retained = nil
	if s.file != nil {
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.file = nil
	}
	return firstErr
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
	return acquireLegacyV18CutoverGateObserved(parent, homeDir, timeout, nil)
}

func acquireLegacyV18CutoverGateObserved(parent context.Context, homeDir string, timeout time.Duration, observe func(string)) (*legacyV18CutoverGate, error) {
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
	source, err := openLegacyV18CutoverPinnedSource(path)
	if err != nil {
		return nil, err
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			_ = source.Close()
		}
	}()

	readDB, err := openLegacyV18CutoverSQLite(path, "ro", timeout, true)
	if err != nil {
		return nil, err
	}
	readConn, err := readDB.Conn(handoffCtx)
	if err != nil {
		_ = readDB.Close()
		return nil, fmt.Errorf("pin legacy v18 cutover SHARED connection: %w", err)
	}
	probeDB, err := openLegacyV18CutoverSQLite(path, "ro", 0, true)
	if err != nil {
		_ = readConn.Close()
		_ = readDB.Close()
		return nil, err
	}
	probeConn, err := probeDB.Conn(handoffCtx)
	if err != nil {
		_ = probeDB.Close()
		_ = readConn.Close()
		_ = readDB.Close()
		return nil, fmt.Errorf("pin legacy v18 cutover reader-barrier probe: %w", err)
	}
	auxiliaryOpen := true
	defer func() {
		if auxiliaryOpen {
			_ = probeConn.Close()
			_ = readConn.Close()
			_ = probeDB.Close()
			_ = readDB.Close()
		}
	}()
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

	readTx, err := readConn.BeginTx(handoffCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin legacy v18 cutover SHARED snapshot: %w", err)
	}
	readTxOpen := true
	defer func() {
		if readTxOpen {
			_ = readTx.Rollback()
		}
	}()

	candidateInfo, err := validateLegacyV18CutoverSource(readTx)
	if err != nil {
		return nil, fmt.Errorf("validate legacy v18 cutover SHARED snapshot: %w", err)
	}
	fleetID, err := legacyV18CutoverFleetID(readTx)
	if err != nil {
		return nil, err
	}
	candidateDigest, err := source.sha256()
	if err != nil {
		return nil, fmt.Errorf("hash legacy v18 cutover SHARED source: %w", err)
	}
	if observe != nil {
		observe("initial source read")
	}

	exclusive := make(chan error, 1)
	go func() {
		_, beginErr := gateConn.ExecContext(handoffCtx, `BEGIN EXCLUSIVE`)
		exclusive <- beginErr
	}()

	exclusiveDone, err := waitForLegacyV18CutoverReaderBarrier(handoffCtx, probeConn, exclusive)
	if err != nil {
		if !exclusiveDone {
			handoffCancel()
			<-exclusive
		}
		return nil, err
	}

	barrierDigest, err := source.sha256()
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
	if observe != nil {
		observe("reader-barrier hash")
	}
	archiveCandidate, err := prepareLegacyV18CutoverArchiveCandidate(homeDir, source, fleetID, candidateDigest)
	if err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("prepare legacy v18 cutover archive candidate under reader barrier: %w", err)
	}
	if observe != nil {
		observe("archive candidate copy")
	}
	barrierPostArchiveDigest, err := source.sha256()
	if err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("rehash legacy v18 source after archive candidate: %w", err)
	}
	if barrierPostArchiveDigest != candidateDigest {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("legacy v18 cutover source changed while archive candidate was written: candidate=%s post_archive=%s", candidateDigest, barrierPostArchiveDigest)
	}

	if err := readTx.Rollback(); err != nil {
		handoffCancel()
		<-exclusive
		return nil, fmt.Errorf("release legacy v18 cutover SHARED snapshot: %w", err)
	}
	readTxOpen = false

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
	postDigest, err := source.sha256()
	if err != nil {
		return nil, fmt.Errorf("hash legacy v18 source under EXCLUSIVE gate: %w", err)
	}
	if postInfo != candidateInfo {
		return nil, fmt.Errorf("legacy v18 cutover source semantic identity changed across EXCLUSIVE handoff: candidate=%+v post=%+v", candidateInfo, postInfo)
	}
	if postDigest != candidateDigest {
		return nil, fmt.Errorf("legacy v18 cutover source changed across EXCLUSIVE handoff: candidate=%s post=%s", candidateDigest, postDigest)
	}
	if archiveCandidate.SHA256 != postDigest {
		return nil, fmt.Errorf("legacy v18 cutover archive candidate digest = %s, source under EXCLUSIVE = %s", archiveCandidate.SHA256, postDigest)
	}
	if observe != nil {
		observe("EXCLUSIVE revalidation")
	}

	gateOpen = false
	auxiliaryOpen = false
	sourceOpen = false
	releaseOnError = false
	return &legacyV18CutoverGate{
		info:             postInfo,
		sourceSHA256:     postDigest,
		archiveCandidate: archiveCandidate,
		source:           source,
		readDB:           readDB,
		readConn:         readConn,
		probeDB:          probeDB,
		probeConn:        probeConn,
		db:               gateDB,
		conn:             gateConn,
		releaseMigration: releaseMigration,
	}, nil
}

func waitForLegacyV18CutoverReaderBarrier(ctx context.Context, probeConn *sql.Conn, exclusive <-chan error) (bool, error) {
	ticker := time.NewTicker(legacyV18CutoverBarrierPoll)
	defer ticker.Stop()
	for {
		var objects int
		err := probeConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects)
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
	firstErr := closeLegacyV18CutoverGateResources(g, true)
	if g.releaseMigration != nil {
		g.releaseMigration()
		g.releaseMigration = nil
	}
	return firstErr
}

func closeLegacyV18CutoverGateResources(g *legacyV18CutoverGate, rollback bool) error {
	var firstErr error
	if rollback && g.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), legacyV18CutoverGateTimeout)
		if _, err := g.conn.ExecContext(ctx, `ROLLBACK`); err != nil {
			firstErr = fmt.Errorf("release legacy v18 cutover EXCLUSIVE gate: %w", err)
		}
		cancel()
	}
	if g.conn != nil {
		if err := g.conn.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close legacy v18 cutover EXCLUSIVE connection: %w", err)
		}
		g.conn = nil
	}
	if g.readConn != nil {
		if err := g.readConn.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close retained legacy v18 cutover SHARED connection: %w", err)
		}
		g.readConn = nil
	}
	if g.probeConn != nil {
		if err := g.probeConn.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close retained legacy v18 cutover reader-barrier probe: %w", err)
		}
		g.probeConn = nil
	}
	if g.db != nil {
		if err := g.db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close legacy v18 cutover EXCLUSIVE database: %w", err)
		}
		g.db = nil
	}
	if g.readDB != nil {
		if err := g.readDB.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close retained legacy v18 cutover SHARED database: %w", err)
		}
		g.readDB = nil
	}
	if g.probeDB != nil {
		if err := g.probeDB.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close retained legacy v18 cutover reader-barrier database: %w", err)
		}
		g.probeDB = nil
	}
	if g.source != nil {
		if err := g.source.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close pinned legacy v18 cutover source: %w", err)
		}
		g.source = nil
	}
	return firstErr
}
