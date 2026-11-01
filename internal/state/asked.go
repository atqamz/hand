package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func askedModel(model, effort string) string {
	if effort == "" {
		return model
	}
	return model + "#" + effort
}

func (s *Store) ModelNote(ctx context.Context, ref, model, effort string) (string, error) {
	prefix := ref + ": "
	var detail string
	err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'model.requested' AND detail LIKE ? ORDER BY seq DESC LIMIT 1`, prefix+"%").Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) || model == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	askedM, askedE, _ := strings.Cut(strings.TrimPrefix(detail, prefix), "#")
	if askedE == "default" {
		askedE = ""
	}
	if askedM == model && (askedE == "" || askedE == effort) {
		return "", nil
	}
	if askedE != "" && effort == "" {
		effort = "default"
	}
	return "Asked for " + strings.Join(strings.Fields(askedM+" "+askedE), " ") + ", running " + strings.Join(strings.Fields(model+" "+effort), " ") + ".", nil
}
