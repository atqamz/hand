package state

import (
	"cmp"
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxDeviceName  = 40
	MaxDeviceLogin = 254
	PhoneLabelLen  = 16
)

type Device struct {
	ID       int64
	Name     string
	Login    string
	Phone    string
	PairedAt time.Time
}

func CleanLogin(s string) string { return cleanDeviceText(s, MaxDeviceLogin) }

func validPhoneLabel(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != PhoneLabelLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func ValidPhoneLabel(s string) bool { return s != "" && validPhoneLabel(s) }

func cleanDeviceText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, ""))), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func (d Device) detail() string { return strconv.FormatInt(d.ID, 10) + " " + d.Name }

func scanDevice(row scanner) (Device, error) {
	var d Device
	var paired string
	if err := row.Scan(&d.ID, &d.Name, &d.Login, &d.Phone, &paired); err != nil {
		return d, err
	}
	var err error
	d.PairedAt, err = time.Parse(time.RFC3339Nano, paired)
	return d, err
}

func (s *Store) AddDevice(ctx context.Context, name, login, secretHash, phone string) (Device, error) {
	d := Device{Name: cleanDeviceText(name, MaxDeviceName), Login: CleanLogin(login), Phone: phone, PairedAt: s.now().UTC()}
	if d.Name == "" {
		d.Name = "Phone"
	}
	if len(secretHash) != 64 {
		return Device{}, fmt.Errorf("%w: a device secret is stored as a 64-character hash", ErrInvalid)
	}
	if !validPhoneLabel(phone) {
		return Device{}, fmt.Errorf("%w: a phone label is empty or %d lowercase hex characters", ErrInvalid, PhoneLabelLen)
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRow(`INSERT INTO device(name, login, secret_hash, phone, paired_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
			d.Name, d.Login, secretHash, d.Phone, d.PairedAt.Format(time.RFC3339Nano)).Scan(&d.ID)
		if err != nil {
			return err
		}
		return emit(tx, d.PairedAt.Format(time.RFC3339Nano), "board.paired", 0, d.detail())
	})
	return d, err
}

func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, login, phone, paired_at FROM device ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeviceWithSecret(ctx context.Context, id int64, secretHash string) (Device, bool, error) {
	var stored string
	row := s.db.QueryRowContext(ctx, `SELECT secret_hash, id, name, login, phone, paired_at FROM device WHERE id = ?`, id)
	var d Device
	var paired string
	err := row.Scan(&stored, &d.ID, &d.Name, &d.Login, &d.Phone, &paired)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(secretHash)) != 1 {
		return Device{}, false, nil
	}
	d.PairedAt, err = time.Parse(time.RFC3339Nano, paired)
	return d, err == nil, err
}

func (s *Store) RevokeDevice(ctx context.Context, id int64) (Device, error) {
	var d Device
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRow(`DELETE FROM device WHERE id = ? RETURNING id, name, login, phone, paired_at`, id)
		var err error
		if d, err = scanDevice(row); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no paired phone %d", ErrNotFound, id)
		} else if err != nil {
			return err
		}
		return emit(tx, s.stamp(), "board.revoked", 0, d.detail())
	})
	return d, err
}

func (s *Store) RevokePhone(ctx context.Context, label string) ([]Device, error) {
	if !ValidPhoneLabel(label) {
		return nil, fmt.Errorf("%w: a phone label is %d lowercase hex characters", ErrInvalid, PhoneLabelLen)
	}
	var out []Device
	err := s.tx(ctx, func(tx *sql.Tx) error {
		out = nil
		rows, err := tx.Query(`DELETE FROM device WHERE phone = ? RETURNING id, name, login, phone, paired_at`, label)
		if err != nil {
			return err
		}
		for rows.Next() {
			d, err := scanDevice(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, d)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		slices.SortFunc(out, func(a, b Device) int { return cmp.Compare(a.ID, b.ID) })
		now := s.stamp()
		for _, d := range out {
			if err := emit(tx, now, "board.revoked", 0, d.detail()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
